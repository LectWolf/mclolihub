import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import ChannelStatusV3View from '../ChannelStatusV3View.vue'
import * as api from '@/api/channelMonitorV2'
import type { MonitorMatrixResponse, MonitorMatrixRow, MonitorMetric, MonitorSnapshot } from '@/api/channelMonitorV2'

vi.mock('@/api/channelMonitorV2', () => ({ getSnapshot: vi.fn(), getMatrix: vi.fn() }))
vi.mock('@/api/groups', () => ({ default: { getAvailable: vi.fn(async () => []), getUserGroupRates: vi.fn(async () => ({})) } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({
  locale: ref('en'),
  t: (key: string, values?: Record<string, unknown>) => {
    if (key === 'channelMonitorV3.cacheRatePeriod') return `${values?.range} average cache`
    if (key === 'channelMonitorV3.summary') return `Availability ${values?.success} · ${values?.range} cache ${values?.cache}`
    return key
  },
}) }))
vi.mock('@/composables/useChannelMonitorFormat', () => ({
  providerGradient: () => '',
  useChannelMonitorFormat: () => ({ providerLabel: (platform: string) => platform, providerBadgeClass: () => '', statusBadgeClass: () => '', statusLabel: (status: string) => status }),
}))

const metric = (cacheRate: number): MonitorMetric => ({
  success_requests: 0, error_requests: 0, request_count: 0, token_count: 0, rpm: 0, tpm: 0,
  error_rate: 0, cache_rate: cacheRate, cache_rate_numerator: 0, cache_rate_denominator: 0,
  ttft: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
  duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
})
const coverage = {
  requested_start: '2026-10-14T09:00:00Z', requested_end: '2026-10-14T10:30:00Z',
  coverage_start: '2026-10-14T09:00:00Z', data_through: '2026-10-14T10:25:00Z',
  computed_at: '2026-10-14T10:30:00Z', aggregation_lag_seconds: 300, coverage_complete: true, bucket_seconds: 300,
}
const health = { overall: 'healthy' as const, error_rate: 'healthy' as const, ttft: 'unknown' as const, minimum_sample: 1 }
const row = (id: number, platform: string, rate: number): MonitorMatrixRow => ({
  group_id: id, group_name: `Group ${id}`, platform, metrics: metric(rate), health,
  buckets: [{ bucket_start: '2026-10-14T10:20:00Z', metrics: metric(0.1), health }],
})
const snapshot = (rate = 0.8): MonitorSnapshot => ({
  config: { version: 1, enabled: true, refresh_interval_seconds: 60, platforms: [], group_ids: [],
    health_thresholds: { minimum_sample: 1, warning_error_rate: 0.05, critical_error_rate: 0.2,
      target_ttft_ms: 3000, warning_ttft_ms: 3000, critical_ttft_ms: 10000, warning_cache_rate: 0.85,
      critical_cache_rate: 0.6, error_weight: 0.6, ttft_weight: 0.2, cache_weight: 0.2 } },
  coverage, metrics: metric(rate), health,
  trend: [{ bucket_start: '2026-10-14T10:20:00Z', metrics: metric(0.1), health }],
})
const matrix = (): MonitorMatrixResponse => ({ coverage, group_by: 'platform_group', items: [row(7, 'openai', 0.9), row(8, 'openai', 0.6), row(9, 'anthropic', 0.4)] })
let wrapper: VueWrapper
const render = async () => {
  wrapper = mount(ChannelStatusV3View, {
    global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' }, Icon: true, ProviderIcon: true,
      ChannelMonitorV3Timeline: true, ChannelMonitorV3QualityPreview: true,
      ChannelMonitorV3HoverTip: { template: '<span><slot /></span>' },
    } },
  })
  await flushPromises()
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getSnapshot).mockReset().mockResolvedValue(snapshot())
  vi.mocked(api.getMatrix).mockReset().mockResolvedValue(matrix())
})
afterEach(() => { wrapper?.unmount() })

describe('channel status V3 filters and range cache rate', () => {
  it('uses selected-window metrics even when public counts are hidden and the latest bucket differs', async () => {
    await render()
    expect(wrapper.findAll('article').map(card => card.get('.mt-5.grid .font-mono').text())).toEqual(['90.0%', '60.0%', '40.0%'])
    expect(wrapper.text()).toContain('90m average cache')
    expect(wrapper.get('[data-testid="monitor-summary"]').text()).toContain('90m cache 80.0%')
  })

  it('filters platforms locally, removes group filters, and requests only weighted summaries', async () => {
    await render()
    expect(wrapper.get('[data-platform-filter="all"]').text()).toContain('3')
    expect(wrapper.get('[data-platform-filter="openai"]').text()).toContain('2')
    vi.mocked(api.getSnapshot).mockResolvedValueOnce(snapshot(0.85))
    await wrapper.get('[data-platform-filter="openai"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(2)
    expect(wrapper.get('[data-platform-filter="openai"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-platform-filter="anthropic"]').exists()).toBe(true)
    expect(wrapper.find('[data-group-filter]').exists()).toBe(false)
    expect(api.getSnapshot).toHaveBeenLastCalledWith({ range: '90m', platforms: ['openai'], groupIds: [], models: [] }, false, expect.any(AbortSignal))
    expect(api.getMatrix).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-testid="monitor-summary"]').text()).toContain('cache 85.0%')

    await wrapper.get('[data-platform-filter="anthropic"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.find('[data-group-filter]').exists()).toBe(false)
    expect(api.getSnapshot).toHaveBeenLastCalledWith({ range: '90m', platforms: ['anthropic'], groupIds: [], models: [] }, false, expect.any(AbortSignal))
  })

  it('loads the new range while keeping filters and fetches the full card matrix', async () => {
    await render()
    await wrapper.get('[data-platform-filter="openai"]').trigger('click')
    await flushPromises()
    vi.mocked(api.getMatrix).mockResolvedValueOnce({ ...matrix(), items: [row(7, 'openai', 0.95), row(8, 'openai', 0.2), row(9, 'anthropic', 0.3)] })
    vi.mocked(api.getSnapshot).mockResolvedValueOnce(snapshot(0.95))
    await wrapper.get('[data-range="24h"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('article').map(card => card.get('.mt-5.grid .font-mono').text())).toEqual(['95.0%', '20.0%'])
    expect(wrapper.text()).toContain('24h average cache')
    expect(api.getSnapshot).toHaveBeenLastCalledWith({ range: '24h', platforms: ['openai'], groupIds: [], models: [] }, false, expect.any(AbortSignal))
    expect(api.getMatrix).toHaveBeenLastCalledWith({ range: '24h', platforms: [], groupIds: [], models: [] }, 'platform_group', false, expect.any(AbortSignal))
    expect(wrapper.find('[data-platform-filter="anthropic"]').exists()).toBe(true)
  })

  it('aborts obsolete summary requests and ignores a late result after filter changes', async () => {
    await render()
    let finish!: (value: MonitorSnapshot) => void
    vi.mocked(api.getSnapshot).mockReturnValueOnce(new Promise(done => { finish = done }))
    await wrapper.get('[data-platform-filter="openai"]').trigger('click')
    const signal = vi.mocked(api.getSnapshot).mock.calls.at(-1)![2]!
    expect(wrapper.find('[data-testid="monitor-summary"]').exists()).toBe(false)
    vi.mocked(api.getSnapshot).mockResolvedValueOnce(snapshot(0.4))
    await wrapper.get('[data-platform-filter="anthropic"]').trigger('click')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    finish(snapshot(0.99))
    await flushPromises()
    expect(wrapper.get('[data-testid="monitor-summary"]').text()).toContain('cache 40.0%')
  })

  it('aborts pending summary work on unmount', async () => {
    await render()
    vi.mocked(api.getSnapshot).mockReturnValueOnce(new Promise(() => {}))
    await wrapper.get('[data-platform-filter="openai"]').trigger('click')
    const signal = vi.mocked(api.getSnapshot).mock.calls.at(-1)![2]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })
})
