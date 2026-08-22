import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import GroupHealthPanel from '../GroupHealthPanel.vue'

const { listGroupHealth } = vi.hoisted(() => ({ listGroupHealth: vi.fn() }))

vi.mock('@/api/groupHealth', () => ({
  listGroupHealth: (...args: unknown[]) => listGroupHealth(...args),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, values?: Record<string, unknown>) => values ? `${key}:${JSON.stringify(values)}` : key,
      locale: { value: 'en-US' },
    }),
  }
})

describe('GroupHealthPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-20T08:00:00Z'))
    listGroupHealth.mockResolvedValue({
      window_hours: 12,
      bucket_minutes: 5,
      items: [{
        group_id: 7,
        name: 'Fast OpenAI',
        platform: 'openai',
        rate_multiplier: 0.5,
        probe_enabled: true,
        probe_model: 'gpt-5.6-sol',
        probe_interval_seconds: 600,
        status: 'balance_insufficient',
        reason: 'no_healthy_account_balance_insufficient',
        last_probe_at: '2026-08-20T07:59:00Z',
        last_success_at: null,
        next_probe_at: null,
        probe_ttft_ms: 1981,
        probe_ttft_avg_ms: 2000,
        probe_ttft_p95_ms: 2200,
        probe_samples: 3,
        probe_availability_6h: 0,
        real_ttft_p50_ms: 5944,
        real_ttft_avg_ms: 6100,
        real_ttft_p95_ms: 7200,
        real_ttft_samples: 4,
        real_total_avg_ms: 9000,
        real_availability_6h: 0,
        cache_rate_overall: 0.62,
        cache_rate_6h: 0.75,
        trend: [
          {
            started_at: '2026-08-20T07:35:00Z',
            probe_success: 1,
            probe_failure: 0,
            real_success: 0,
            real_failure: 0,
            probe_ttft_ms: 1377,
            real_ttft_ms: 0,
          },
          {
            started_at: '2026-08-20T07:45:00Z',
            probe_success: 1,
            probe_failure: 1,
            real_success: 0,
            real_failure: 0,
            probe_ttft_ms: 35_000,
            real_ttft_ms: 0,
          },
          {
            started_at: '2026-08-20T07:55:00Z',
            probe_success: 0,
            probe_failure: 1,
            real_success: 0,
            real_failure: 1,
            probe_ttft_ms: 0,
            real_ttft_ms: 0,
          },
        ],
      }],
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('renders status, routing metrics, zero availability and a 72-bucket trend', async () => {
    const wrapper = mount(GroupHealthPanel, {
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('Fast OpenAI')
    expect(wrapper.text()).toContain('0.50x')
    expect(wrapper.text()).toContain('groupHealth.statuses.balance_insufficient')
    expect(wrapper.text()).toContain('5944 ms')
    expect(wrapper.text()).toContain('0.0%')
    expect(wrapper.text()).toContain('groupHealth.cacheRateOverallShort:{"value":"62.0%"}')
    expect(wrapper.find('[aria-label]').attributes('aria-label')).toContain('62.0%')
    expect(wrapper.find('[aria-label]').attributes('aria-label')).toContain('75.0%')
    expect(wrapper.find('.badge').classes()).toContain('badge-info')
    expect(wrapper.findAll('td:last-child > div > span')).toHaveLength(72)
    const healthy = wrapper.get('[data-probe-status="healthy"]')
    const slow = wrapper.get('[data-probe-status="slow"]')
    const unavailable = wrapper.get('[data-probe-status="unavailable"]')
    expect(healthy.classes()).toContain('bg-emerald-500')
    expect(slow.classes()).toContain('bg-amber-400')
    expect(unavailable.classes()).toContain('bg-red-500')
    expect(Number.parseFloat(healthy.attributes('style').match(/height:\s*([\d.]+)%/)?.[1] || '0')).toBeGreaterThanOrEqual(100 / 15)
    expect(Number.parseFloat(slow.attributes('style').match(/height:\s*([\d.]+)%/)?.[1] || '0')).toBe(100)
    expect(Number.parseFloat(unavailable.attributes('style').match(/height:\s*([\d.]+)%/)?.[1] || '0')).toBeCloseTo(100 / 15, 4)

    wrapper.unmount()
  })

  it('sorts available groups first and then by ascending multiplier by default', async () => {
    const item = (group_id: number, name: string, status: string, rate_multiplier: number) => ({
      group_id,
      name,
      status,
      rate_multiplier,
      platform: 'openai',
      probe_enabled: status !== 'not_enabled',
      trend: [],
    })
    listGroupHealth.mockResolvedValue({
      window_hours: 12,
      bucket_minutes: 10,
      items: [
        item(1, 'Unavailable Cheap', 'unavailable', 0.01),
        item(2, 'Healthy Expensive', 'healthy', 0.5),
        item(3, 'Healthy Cheap', 'healthy', 0.1),
        item(4, 'Probe Disabled', 'not_enabled', 0.05),
        item(5, 'No Balance', 'balance_insufficient', 0.001),
      ],
    })

    const wrapper = mount(GroupHealthPanel, {
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    expect(wrapper.findAll('tbody tr strong').map(node => node.text())).toEqual([
      'Probe Disabled',
      'Healthy Cheap',
      'Healthy Expensive',
      'No Balance',
      'Unavailable Cheap',
    ])

    wrapper.unmount()
  })
})
