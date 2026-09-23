import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import QoderQuotaCell from '../QoderQuotaCell.vue'
import UsageProgressBar from '../UsageProgressBar.vue'
import type { QoderQuotaSnapshot } from '@/api/admin/accounts'
import type { Account } from '@/types'

const { getQoderQuota } = vi.hoisted(() => ({
  getQoderQuota: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getQoderQuota
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const snapshot = (overrides: Partial<QoderQuotaSnapshot> = {}): QoderQuotaSnapshot => ({
  plan_type: 'personal_standard',
  unit: 'credits',
  exceeded: false,
  resets_at: '2026-10-01T00:00:00Z',
  credential: 'device',
  checked_at: new Date().toISOString(),
  pools: [
    { kind: 'plan', total: 5000, used: 1850, remaining: 3150, available: true },
    { kind: 'org', total: 76000, used: 13629, remaining: 62371, available: true }
  ],
  ...overrides
})

const account = (extra: Record<string, unknown> = {}, platform: Account['platform'] = 'qoder'): Account => ({
  id: 21,
  name: 'qoder',
  platform,
  type: 'apikey',
  extra,
  proxy_id: null,
  concurrency: 1,
  priority: 1,
  status: 'active',
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-09-22T00:00:00Z',
  updated_at: '2026-09-22T00:00:00Z',
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null
})

describe('QoderQuotaCell', () => {
  beforeEach(() => {
    getQoderQuota.mockReset()
  })

  it('renders the stored snapshot without calling Qoder', () => {
    const wrapper = mount(QoderQuotaCell, { props: { account: account({ qoder_quota: snapshot() }) } })

    expect(wrapper.find('[data-test="qoder-quota-exceeded"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.accounts.qoderQuota.remaining')
    const bars = wrapper.findAllComponents(UsageProgressBar)
    expect(bars).toHaveLength(2)
    expect(bars[0].props()).toMatchObject({
      label: 'admin.accounts.qoderQuota.pools.plan',
      utilization: 37,
      resetsAt: '2026-10-01T00:00:00Z',
      color: 'emerald'
    })
    // The top-level reset time belongs to the plan pool only.
    expect(bars[1].props()).toMatchObject({ label: 'admin.accounts.qoderQuota.pools.org', resetsAt: null, color: 'purple' })
    expect(getQoderQuota).not.toHaveBeenCalled()
  })

  it('refreshes from Qoder and shows the new snapshot', async () => {
    getQoderQuota.mockResolvedValueOnce(
      snapshot({ exceeded: true, pools: [{ kind: 'plan', total: 100, used: 100, remaining: 0, available: true }] })
    )
    const wrapper = mount(QoderQuotaCell, { props: { account: account() } })
    expect(wrapper.text()).toContain('admin.accounts.qoderQuota.never')

    await wrapper.get('[data-test="qoder-quota-refresh"]').trigger('click')
    await flushPromises()

    expect(getQoderQuota).toHaveBeenCalledWith(21, true)
    expect(wrapper.find('[data-test="qoder-quota-exceeded"]').exists()).toBe(true)
    const bars = wrapper.findAllComponents(UsageProgressBar)
    expect(bars).toHaveLength(1)
    expect(bars[0].props('utilization')).toBe(100)
  })

  it('shows the API error message when the query fails', async () => {
    getQoderQuota.mockRejectedValueOnce({ status: 502, message: 'Qoder refused the quota query' })
    const wrapper = mount(QoderQuotaCell, { props: { account: account() } })

    await wrapper.get('[data-test="qoder-quota-refresh"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Qoder refused the quota query')
  })

  it('collapses pools beyond the first three', () => {
    const pools = Array.from({ length: 5 }, (_, i) => ({
      kind: 'package',
      name: `pack-${i}`,
      total: 10,
      used: i,
      remaining: 10 - i,
      available: true
    }))
    const wrapper = mount(QoderQuotaCell, { props: { account: account({ qoder_quota: snapshot({ pools }) }) } })

    const bars = wrapper.findAllComponents(UsageProgressBar)
    expect(bars).toHaveLength(3)
    expect(bars[0].props()).toMatchObject({ label: 'pack-0', labelWidth: 'auto' })
    expect(wrapper.text()).toContain('admin.accounts.qoderQuota.morePools')
  })

  it('renders nothing for other platforms', () => {
    const wrapper = mount(QoderQuotaCell, { props: { account: account({}, 'openai') } })
    expect(wrapper.find('[data-test="qoder-quota"]').exists()).toBe(false)
  })
})
