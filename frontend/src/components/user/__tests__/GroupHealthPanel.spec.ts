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
        trend: [{
          started_at: '2026-08-20T07:55:00Z',
          probe_success: 0,
          probe_failure: 1,
          real_success: 0,
          real_failure: 1,
          probe_ttft_ms: 0,
          real_ttft_ms: 0,
        }],
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
    expect(wrapper.find('.badge').classes()).toContain('badge-info')
    expect(wrapper.findAll('td:last-child > div > span')).toHaveLength(72)

    wrapper.unmount()
  })
})
