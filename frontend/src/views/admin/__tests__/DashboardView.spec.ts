import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { DashboardStats } from '@/types'
import DashboardView from '../DashboardView.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'

const { getSnapshotV2, getUserUsageTrend, getUserSpendingRanking } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key} ${JSON.stringify(params)}` : key
    })
  }
})

const AUTO_REFRESH_STORAGE_KEY = 'admin-dashboard-auto-refresh'

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const createDashboardStats = (overrides: Partial<DashboardStats> = {}): DashboardStats => ({
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_cost: 0,
  total_actual_cost: 0,
  total_account_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  today_account_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0,
  ...overrides
})

const mountDashboard = () =>
  mount(DashboardView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        RouterLink: { props: ['to'], template: '<a><slot /></a>' },
        LoadingSpinner: true,
        Icon: true,
        HelpTooltip: true,
        AutoRefreshButton: true,
        DateRangePicker: true,
        Select: true,
        ModelDistributionChart: true,
        GroupDistributionChart: true,
        TokenUsageTrend: true,
        SpendTrendChart: true,
        Line: true
      }
    }
  })

const tileByLabel = (wrapper: ReturnType<typeof mountDashboard>, label: string) => {
  const tile = wrapper.findAll('.card').find((card) => card.text().includes(label))
  if (!tile) throw new Error(`tile ${label} not found`)
  return tile
}

describe('admin DashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.removeItem(AUTO_REFRESH_STORAGE_KEY)

    getSnapshotV2.mockReset()
    getUserUsageTrend.mockReset()
    getUserSpendingRanking.mockReset()

    getSnapshotV2.mockResolvedValue({
      generated_at: '2026-09-23T08:00:00Z',
      stats: createDashboardStats(),
      trend: [],
      models: [],
      groups: []
    })
    getUserUsageTrend.mockResolvedValue({
      trend: [],
      start_date: '',
      end_date: '',
      granularity: 'hour'
    })
    getUserSpendingRanking.mockResolvedValue({
      ranking: [],
      total_actual_cost: 0,
      total_requests: 0,
      total_tokens: 0,
      start_date: '',
      end_date: ''
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    localStorage.removeItem(AUTO_REFRESH_STORAGE_KEY)
  })

  it('uses last 24 hours as default dashboard range', async () => {
    mountDashboard()

    await flushPromises()

    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: formatLocalDate(yesterday),
      end_date: formatLocalDate(now),
      granularity: 'hour',
      include_stats: true,
      include_group_stats: true
    }))
  })

  it('shows spend, profit and same-period deltas for today', async () => {
    getSnapshotV2.mockResolvedValue({
      generated_at: '2026-09-23T08:00:00Z',
      stats: createDashboardStats({
        today_actual_cost: 12.5,
        today_account_cost: 7.5,
        today_cost: 20,
        today_requests: 1200,
        yesterday_requests: 1500,
        yesterday_actual_cost: 10,
        yesterday_account_cost: 6,
        yesterday_tokens: 0,
        total_accounts: 4,
        account_health: {
          available: 3,
          rate_limited: 1,
          temp_unschedulable: 0,
          unschedulable: 0,
          error: 0,
          balance_insufficient: 0,
          inactive: 0,
          other: 0
        }
      }),
      trend: [],
      models: [],
      groups: []
    })

    const wrapper = mountDashboard()
    await flushPromises()

    const spend = tileByLabel(wrapper, 'admin.dashboard.todaySpend')
    expect(spend.get('[data-testid="stat-value"]').text()).toBe('$12.50')
    expect(spend.get('[data-testid="stat-delta"]').text()).toBe('25%')
    expect(spend.get('[data-testid="stat-delta"]').classes()).toContain('text-emerald-700')

    // 毛利 = 12.5 - 7.5 = 5；昨日同时段毛利 = 10 - 6 = 4 → +25%
    const profit = tileByLabel(wrapper, 'admin.dashboard.todayProfit')
    expect(profit.get('[data-testid="stat-value"]').text()).toBe('$5.00')
    expect(profit.get('[data-testid="stat-delta"]').text()).toBe('25%')
    expect(profit.text()).toContain('40.0%')

    const requests = tileByLabel(wrapper, 'admin.dashboard.todayRequests')
    expect(requests.get('[data-testid="stat-delta"]').text()).toBe('20%')
    expect(requests.get('[data-testid="stat-delta"]').classes()).toContain('text-red-700')

    // 昨日同时段为 0 时不给出百分比
    const tokens = tileByLabel(wrapper, 'admin.dashboard.todayTokens')
    expect(tokens.find('[data-testid="stat-delta"]').exists()).toBe(false)

    expect(wrapper.get('[data-testid="account-available"]').text()).toContain('3')
    expect(wrapper.find('[data-testid="account-health-rate_limited"]').exists()).toBe(true)
  })

  it('hides deltas for payloads without same-period fields', async () => {
    getSnapshotV2.mockResolvedValue({
      generated_at: '2026-09-23T08:00:00Z',
      stats: createDashboardStats({ today_actual_cost: 3, today_requests: 10 }),
      trend: [],
      models: [],
      groups: []
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.find('[data-testid="stat-delta"]').exists()).toBe(false)
  })

  it('flags stale stats only when the aggregation watermark is real', async () => {
    getSnapshotV2.mockResolvedValueOnce({
      generated_at: '2026-09-23T08:00:00Z',
      stats: createDashboardStats({ stats_stale: true, stats_updated_at: '1970-01-01T00:00:00Z' }),
      trend: [],
      models: [],
      groups: []
    })
    const wrapper = mountDashboard()
    await flushPromises()
    expect(wrapper.find('[data-testid="stats-stale"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stats-generated-at"]').exists()).toBe(true)

    getSnapshotV2.mockResolvedValueOnce({
      generated_at: '2026-09-23T08:00:00Z',
      stats: createDashboardStats({ stats_stale: true, stats_updated_at: '2026-09-23T07:40:00Z' }),
      trend: [],
      models: [],
      groups: []
    })
    const staleWrapper = mountDashboard()
    await flushPromises()
    expect(staleWrapper.find('[data-testid="stats-stale"]').exists()).toBe(true)
  })

  it('auto refresh reloads silently without flashing loading states', async () => {
    localStorage.setItem(AUTO_REFRESH_STORAGE_KEY, JSON.stringify({ enabled: true, interval_seconds: 30 }))
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })

    const wrapper = mountDashboard()
    await flushPromises()
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)

    let resolveRefresh: (value: unknown) => void = () => {}
    getSnapshotV2.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveRefresh = resolve
        })
    )

    vi.advanceTimersByTime(31_000)
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenCalledTimes(2)
    expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({ include_stats: true }))
    // 刷新进行中：图表不切成加载态，也不降低不透明度
    expect(wrapper.findComponent(TokenUsageTrend).props('loading')).toBe(false)
    expect(wrapper.find('.opacity-60').exists()).toBe(false)

    resolveRefresh({
      generated_at: '2026-09-23T08:01:00Z',
      stats: createDashboardStats({ today_requests: 42 }),
      trend: [],
      models: [],
      groups: []
    })
    await flushPromises()

    const requests = tileByLabel(wrapper, 'admin.dashboard.todayRequests')
    expect(requests.get('[data-testid="stat-value"]').text()).toBe('42')
  })
})
