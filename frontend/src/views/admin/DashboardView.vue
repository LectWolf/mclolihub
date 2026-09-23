<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <template v-else-if="stats">
        <!-- Today Overview -->
        <section class="space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="min-w-0">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('admin.dashboard.todayOverview') }}
              </h2>
              <p class="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
                <span>{{ t('admin.dashboard.deltaCaption') }}</span>
                <span v-if="generatedAtLabel" data-testid="stats-generated-at">
                  · {{ t('admin.dashboard.statsUpdatedAt', { time: generatedAtLabel }) }}
                </span>
                <span
                  v-if="statsLagging"
                  data-testid="stats-stale"
                  class="inline-flex items-center gap-1 rounded-full bg-amber-50 px-2 py-0.5 font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
                  :title="statsStaleHint"
                >
                  <Icon name="exclamationTriangle" size="xs" :stroke-width="2" />
                  {{ t('admin.dashboard.statsStale') }}
                </span>
              </p>
            </div>
            <div class="flex items-center gap-2">
              <AutoRefreshButton
                :enabled="autoRefresh.enabled.value"
                :interval-seconds="autoRefresh.intervalSeconds.value"
                :countdown="autoRefresh.countdown.value"
                :intervals="autoRefresh.intervals"
                @update:enabled="autoRefresh.setEnabled"
                @update:interval="autoRefresh.setInterval"
              />
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="refreshing"
                @click="refreshAll()"
              >
                <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" />
                {{ t('common.refresh') }}
              </button>
            </div>
          </div>

          <!-- Row 1: today's business -->
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <DashboardStatTile
              :label="t('admin.dashboard.todaySpend')"
              :hint="t('admin.dashboard.todaySpendHint')"
              icon="dollar"
              icon-class="bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400"
              :value="formatUsd(stats.today_actual_cost)"
              :delta="spendDelta"
              :delta-hint="yesterdayHint(stats.yesterday_actual_cost, formatUsd)"
            >
              <p>
                {{ t('admin.dashboard.standardCostValue', { value: formatUsd(stats.today_cost) }) }}
                · {{ t('admin.dashboard.cumulativeValue', { value: formatUsd(stats.total_actual_cost) }) }}
              </p>
            </DashboardStatTile>

            <DashboardStatTile
              :label="t('admin.dashboard.todayProfit')"
              :hint="t('admin.dashboard.todayProfitHint')"
              icon="trendingUp"
              icon-class="bg-blue-100 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400"
              :value="formatUsd(todayProfit)"
              :value-class="todayProfit < 0 ? '!text-red-600 dark:!text-red-400' : ''"
              :delta="profitDelta"
              :delta-hint="yesterdayHint(yesterdayProfit, formatUsd)"
            >
              <p>
                {{ t('admin.dashboard.accountCostValue', { value: formatUsd(stats.today_account_cost) }) }}
                · {{ t('admin.dashboard.marginValue', { value: formatPercent(todayMargin) }) }}
              </p>
            </DashboardStatTile>

            <DashboardStatTile
              :label="t('admin.dashboard.todayRequests')"
              icon="chart"
              icon-class="bg-violet-100 text-violet-600 dark:bg-violet-900/30 dark:text-violet-400"
              :value="formatInteger(stats.today_requests)"
              :delta="requestsDelta"
              :delta-hint="yesterdayHint(stats.yesterday_requests, formatInteger)"
            >
              <p>{{ t('admin.dashboard.cumulativeValue', { value: formatInteger(stats.total_requests) }) }}</p>
            </DashboardStatTile>

            <DashboardStatTile
              :label="t('admin.dashboard.todayTokens')"
              icon="cube"
              icon-class="bg-amber-100 text-amber-600 dark:bg-amber-900/30 dark:text-amber-400"
              :value="formatTokens(stats.today_tokens)"
              :value-title="tokenBreakdown"
              :delta="tokensDelta"
              :delta-hint="yesterdayHint(stats.yesterday_tokens, formatTokens)"
            >
              <p>
                {{ t('admin.dashboard.cacheHitValue', { value: formatPercent(todayCacheHitRate) }) }}
                · {{ t('admin.dashboard.cumulativeValue', { value: formatTokens(stats.total_tokens) }) }}
              </p>
            </DashboardStatTile>
          </div>

          <!-- Row 2: resources & live load -->
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <AccountHealthCard
              class="sm:col-span-2"
              :health="accountHealth"
              :total="toFiniteNumber(stats.total_accounts)"
              :normal-fallback="toFiniteNumber(stats.normal_accounts)"
              :error-fallback="toFiniteNumber(stats.error_accounts)"
              :overloaded="toFiniteNumber(stats.overload_accounts)"
            />

            <DashboardStatTile
              :label="t('admin.dashboard.usersCard')"
              icon="users"
              icon-class="bg-sky-100 text-sky-600 dark:bg-sky-900/30 dark:text-sky-400"
              :value="formatInteger(stats.total_users)"
            >
              <p>
                <span class="font-medium text-emerald-700 dark:text-emerald-400">
                  {{ t('admin.dashboard.newUsersValue', { n: formatInteger(stats.today_new_users) }) }}
                </span>
                · {{ t('admin.dashboard.activeUsersValue', { n: formatInteger(stats.active_users) }) }}
                · {{ t('admin.dashboard.hourlyActiveUsersValue', { n: formatInteger(stats.hourly_active_users) }) }}
              </p>
              <p>
                {{ t('admin.dashboard.apiKeysValue', {
                  total: formatInteger(stats.total_api_keys),
                  active: formatInteger(stats.active_api_keys)
                }) }}
              </p>
            </DashboardStatTile>

            <DashboardStatTile
              :label="t('admin.dashboard.realtimeLoad')"
              :hint="t('admin.dashboard.realtimeLoadHint')"
              icon="bolt"
              icon-class="bg-rose-100 text-rose-600 dark:bg-rose-900/30 dark:text-rose-400"
              :value="formatTokens(stats.rpm)"
              unit="RPM"
            >
              <p>
                {{ formatTokens(stats.tpm) }} TPM
                · {{ t('admin.dashboard.todayAvgResponseValue', { value: formatDuration(todayAverageDuration) }) }}
              </p>
              <p v-if="stats.uptime > 0">{{ t('admin.dashboard.uptimeValue', { value: uptimeLabel }) }}</p>
            </DashboardStatTile>
          </div>
        </section>

        <!-- Analytics -->
        <section class="space-y-4">
          <div class="card p-4">
            <div class="flex flex-wrap items-center gap-4">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('admin.dashboard.analysisTitle') }}
              </h2>
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.timeRange') }}:</span
                >
                <DateRangePicker
                  v-model:start-date="startDate"
                  v-model:end-date="endDate"
                  @change="onDateRangeChange"
                />
              </div>
              <div class="ml-auto flex items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.granularity') }}:</span
                >
                <div class="w-28">
                  <Select
                    v-model="granularity"
                    :options="granularityOptions"
                    @change="loadChartData"
                  />
                </div>
              </div>
            </div>
          </div>

          <!-- 重新加载时保留上一帧，只降低不透明度，避免图表闪成加载态 -->
          <div class="space-y-6 transition-opacity" :class="chartsRefreshing ? 'opacity-60' : ''">
            <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
              <ModelDistributionChart
                :model-stats="modelStats"
                :enable-ranking-view="true"
                :ranking-items="rankingItems"
                :ranking-total-actual-cost="rankingTotalActualCost"
                :ranking-total-requests="rankingTotalRequests"
                :ranking-total-tokens="rankingTotalTokens"
                :loading="chartsLoading && modelStats.length === 0"
                :ranking-loading="rankingLoading && rankingItems.length === 0"
                :ranking-error="rankingError"
                :start-date="startDate"
                :end-date="endDate"
                @ranking-click="goToUserUsage"
              />
              <GroupDistributionChart
                :group-stats="groupStats"
                :loading="chartsLoading && groupStats.length === 0"
                :start-date="startDate"
                :end-date="endDate"
              />
            </div>

            <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
              <TokenUsageTrend :trend-data="trendData" :loading="chartsLoading && trendData.length === 0" />
              <SpendTrendChart :trend-data="trendData" :loading="chartsLoading && trendData.length === 0" />
            </div>

            <!-- User Usage Trend (Full Width) -->
            <div class="card p-4">
              <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.dashboard.userUsageTrend') }}
              </h3>
              <div class="h-64">
                <div v-if="userTrendLoading && !userTrendChartData" class="flex h-full items-center justify-center">
                  <LoadingSpinner size="md" />
                </div>
                <Line v-else-if="userTrendChartData" :data="userTrendChartData" :options="lineOptions" />
                <div
                  v-else
                  class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400"
                >
                  {{ t('admin.dashboard.noDataAvailable') }}
                </div>
              </div>
            </div>
          </div>
        </section>

        <!-- Quick Actions -->
        <div class="card p-4">
          <div class="mb-3 flex items-center justify-between">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.quickActions') }}
            </h2>
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <button
              v-if="canUseBatchImage"
              type="button"
              class="group flex items-center gap-3 rounded-lg bg-gray-50 p-3 text-left transition-colors hover:bg-sky-50 dark:bg-dark-800/50 dark:hover:bg-sky-900/20"
              @click="router.push('/batch-image')"
            >
              <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-sky-100 text-sky-600 dark:bg-sky-900/30 dark:text-sky-400">
                <Icon name="sparkles" size="md" :stroke-width="2" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.dashboard.batchImage') }}
                </span>
                <span class="block text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.batchImageDesc') }}
                </span>
              </span>
              <Icon name="chevronRight" size="sm" class="text-gray-400 group-hover:text-sky-500" />
            </button>
            <button
              type="button"
              class="group flex items-center gap-3 rounded-lg bg-gray-50 p-3 text-left transition-colors hover:bg-emerald-50 dark:bg-dark-800/50 dark:hover:bg-emerald-900/20"
              @click="router.push('/admin/groups')"
            >
              <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400">
                <Icon name="grid" size="md" :stroke-width="2" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.dashboard.groupPricing') }}
                </span>
                <span class="block text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.groupPricingDesc') }}
                </span>
              </span>
              <Icon name="chevronRight" size="sm" class="text-gray-400 group-hover:text-emerald-500" />
            </button>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
import { adminAPI } from '@/api/admin'
import type {
  DashboardStats,
  TrendDataPoint,
  ModelStat,
  GroupStat,
  UserUsageTrendPoint,
  UserSpendingRankingItem
} from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import AutoRefreshButton from '@/components/common/AutoRefreshButton.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import GroupDistributionChart from '@/components/charts/GroupDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import DashboardStatTile from '@/components/admin/dashboard/DashboardStatTile.vue'
import AccountHealthCard from '@/components/admin/dashboard/AccountHealthCard.vue'
import SpendTrendChart from '@/components/admin/dashboard/SpendTrendChart.vue'
import {
  cacheHitRate,
  computeDelta,
  formatDuration,
  formatPercent,
  formatTokens,
  formatUptime,
  formatUsd,
  isMeaningfulTimestamp,
  profitMargin,
  resolveAccountHealth,
  toFiniteNumber
} from '@/components/admin/dashboard/dashboardMetrics'
import { trendAxisTicks } from '@/components/charts/trendAxis'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { formatDateTime, formatTime } from '@/utils/format'

import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'

// Register Chart.js components
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
)

const appStore = useAppStore()
const router = useRouter()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()
const stats = ref<DashboardStats | null>(null)
const generatedAt = ref('')
const loading = ref(false)
const refreshing = ref(false)
const chartsLoading = ref(false)
const userTrendLoading = ref(false)
const rankingLoading = ref(false)
const rankingError = ref(false)

// Chart data
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const groupStats = ref<GroupStat[]>([])
const userTrend = ref<UserUsageTrendPoint[]>([])
const rankingItems = ref<UserSpendingRankingItem[]>([])
const rankingTotalActualCost = ref(0)
const rankingTotalRequests = ref(0)
const rankingTotalTokens = ref(0)
let chartLoadSeq = 0
let usersTrendLoadSeq = 0
let rankingLoadSeq = 0
const rankingLimit = 12

interface LoadOptions {
  /** 静默刷新（自动刷新）：不切换加载态、不弹错误提示，保持当前画面 */
  silent?: boolean
}

// Helper function to format date in local timezone
const formatLocalDate = (date: Date): string => {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

const getLast24HoursRangeDates = (): { start: string; end: string } => {
  const end = new Date()
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return {
    start: formatLocalDate(start),
    end: formatLocalDate(end)
  }
}

// Date range
const granularity = ref<'day' | 'hour'>('hour')
const defaultRange = getLast24HoursRangeDates()
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)

// Granularity options for Select component
const granularityOptions = computed(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') }
])

const chartsRefreshing = computed(
  () => chartsLoading.value || userTrendLoading.value || rankingLoading.value
)

// Today overview metrics
const todayProfit = computed(
  () => toFiniteNumber(stats.value?.today_actual_cost) - toFiniteNumber(stats.value?.today_account_cost)
)
const yesterdayProfit = computed(
  () => toFiniteNumber(stats.value?.yesterday_actual_cost) - toFiniteNumber(stats.value?.yesterday_account_cost)
)
const todayMargin = computed(() => profitMargin(stats.value?.today_actual_cost, stats.value?.today_account_cost))
const todayCacheHitRate = computed(() =>
  cacheHitRate(
    stats.value?.today_input_tokens,
    stats.value?.today_cache_read_tokens,
    stats.value?.today_cache_creation_tokens
  )
)
const todayAverageDuration = computed(
  () => stats.value?.today_average_duration_ms ?? stats.value?.average_duration_ms ?? 0
)
const accountHealth = computed(() => resolveAccountHealth(stats.value))

// 旧版后端没有昨日同时段字段时不展示涨跌幅
const hasYesterdayStats = computed(() => stats.value?.yesterday_requests !== undefined)
const spendDelta = computed(() =>
  hasYesterdayStats.value ? computeDelta(stats.value?.today_actual_cost, stats.value?.yesterday_actual_cost) : null
)
const profitDelta = computed(() =>
  hasYesterdayStats.value ? computeDelta(todayProfit.value, yesterdayProfit.value) : null
)
const requestsDelta = computed(() =>
  hasYesterdayStats.value ? computeDelta(stats.value?.today_requests, stats.value?.yesterday_requests) : null
)
const tokensDelta = computed(() =>
  hasYesterdayStats.value ? computeDelta(stats.value?.today_tokens, stats.value?.yesterday_tokens) : null
)

const yesterdayHint = (value: number | undefined, format: (value: number) => string): string => {
  const previous = toFiniteNumber(value)
  if (previous === 0) return t('admin.dashboard.noYesterdayData')
  return t('admin.dashboard.yesterdaySamePeriod', { value: format(previous) })
}

const tokenBreakdown = computed(() =>
  t('admin.dashboard.tokenBreakdown', {
    input: formatTokens(stats.value?.today_input_tokens),
    output: formatTokens(stats.value?.today_output_tokens),
    cacheWrite: formatTokens(stats.value?.today_cache_creation_tokens),
    cacheRead: formatTokens(stats.value?.today_cache_read_tokens)
  })
)

const uptimeLabel = computed(() =>
  formatUptime(stats.value?.uptime, {
    day: t('admin.dashboard.uptimeUnits.day'),
    hour: t('admin.dashboard.uptimeUnits.hour'),
    minute: t('admin.dashboard.uptimeUnits.minute')
  })
)

const generatedAtLabel = computed(() => (generatedAt.value ? formatTime(generatedAt.value) : ''))
// 预聚合水位是真实时间且已过期时才提示延迟（未启用预聚合时水位为 1970 年，统计直接来自明细）
const statsLagging = computed(
  () => Boolean(stats.value?.stats_stale) && isMeaningfulTimestamp(stats.value?.stats_updated_at)
)
const statsStaleHint = computed(() =>
  t('admin.dashboard.statsStaleHint', { time: formatDateTime(stats.value?.stats_updated_at) })
)

// Dark mode detection
const isDarkMode = computed(() => {
  return document.documentElement.classList.contains('dark')
})

// Chart colors
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb'
}))

// Line chart options (for user trend chart)
const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const
  },
  plugins: {
    legend: {
      position: 'top' as const,
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      itemSort: (a: any, b: any) => {
        const aValue = typeof a?.raw === 'number' ? a.raw : Number(a?.parsed?.y ?? 0)
        const bValue = typeof b?.raw === 'number' ? b.raw : Number(b?.parsed?.y ?? 0)
        return bValue - aValue
      },
      callbacks: {
        label: (context: any) => {
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        }
      }
    }
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        ...trendAxisTicks,
        color: chartColors.value.text,
        font: {
          size: 10
        }
      }
    },
    y: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        },
        callback: (value: string | number) => formatTokens(Number(value))
      }
    }
  }
}))

// User trend chart data
const userTrendChartData = computed(() => {
  if (!userTrend.value?.length) return null

  const getDisplayName = (point: UserUsageTrendPoint): string => {
    const username = point.username?.trim()
    if (username) {
      return username
    }

    const email = point.email?.trim()
    if (email) {
      return email
    }

    return t('admin.redeem.userPrefix', { id: point.user_id })
  }

  // Group by user_id to avoid merging different users with the same display name
  const userGroups = new Map<number, { name: string; data: Map<string, number> }>()
  const allDates = new Set<string>()

  userTrend.value.forEach((point) => {
    allDates.add(point.date)
    const key = point.user_id
    if (!userGroups.has(key)) {
      userGroups.set(key, { name: getDisplayName(point), data: new Map() })
    }
    userGroups.get(key)!.data.set(point.date, point.tokens)
  })

  const sortedDates = Array.from(allDates).sort()
  const colors = [
    '#3b82f6',
    '#10b981',
    '#f59e0b',
    '#ef4444',
    '#8b5cf6',
    '#ec4899',
    '#14b8a6',
    '#f97316',
    '#6366f1',
    '#84cc16',
    '#06b6d4',
    '#a855f7'
  ]

  const datasets = Array.from(userGroups.values()).map((group, idx) => ({
    label: group.name,
    data: sortedDates.map((date) => group.data.get(date) || 0),
    borderColor: colors[idx % colors.length],
    backgroundColor: `${colors[idx % colors.length]}20`,
    fill: false,
    tension: 0.3
  }))

  return {
    labels: sortedDates,
    datasets
  }
})

const formatInteger = (value: number | null | undefined): string => {
  return toFiniteNumber(value).toLocaleString()
}

const goToUserUsage = (item: UserSpendingRankingItem) => {
  void router.push({
    path: '/admin/usage',
    query: {
      user_id: String(item.user_id),
      start_date: startDate.value,
      end_date: endDate.value
    }
  })
}

// Date range change handler
const onDateRangeChange = (range: {
  startDate: string
  endDate: string
  preset: string | null
}) => {
  // Auto-select granularity based on date range
  const start = new Date(range.startDate)
  const end = new Date(range.endDate)
  const daysDiff = Math.ceil((end.getTime() - start.getTime()) / (1000 * 60 * 60 * 24))

  // If range is 1 day, use hourly granularity
  if (daysDiff <= 1) {
    granularity.value = 'hour'
  } else {
    granularity.value = 'day'
  }

  loadChartData()
}

// Load data
const loadDashboardSnapshot = async (includeStats: boolean, { silent = false }: LoadOptions = {}) => {
  const currentSeq = ++chartLoadSeq
  if (includeStats && !stats.value) {
    loading.value = true
  }
  if (!silent) {
    chartsLoading.value = true
  }
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      include_stats: includeStats,
      include_trend: true,
      include_model_stats: true,
      include_group_stats: true,
      include_users_trend: false
    })
    if (currentSeq !== chartLoadSeq) return
    if (includeStats && response.stats) {
      stats.value = response.stats
      generatedAt.value = response.generated_at || ''
    }
    trendData.value = response.trend || []
    modelStats.value = response.models || []
    groupStats.value = response.groups || []
  } catch (error) {
    if (currentSeq !== chartLoadSeq) return
    if (!silent) {
      appStore.showError(t('admin.dashboard.failedToLoad'))
    }
    console.error('Error loading dashboard snapshot:', error)
  } finally {
    if (currentSeq === chartLoadSeq) {
      loading.value = false
      chartsLoading.value = false
    }
  }
}

const loadUsersTrend = async ({ silent = false }: LoadOptions = {}) => {
  const currentSeq = ++usersTrendLoadSeq
  if (!silent) {
    userTrendLoading.value = true
  }
  try {
    const response = await adminAPI.dashboard.getUserUsageTrend({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      limit: 12
    })
    if (currentSeq !== usersTrendLoadSeq) return
    userTrend.value = response.trend || []
  } catch (error) {
    if (currentSeq !== usersTrendLoadSeq) return
    console.error('Error loading users trend:', error)
    // 静默刷新失败时保留上一帧数据
    if (!silent) {
      userTrend.value = []
    }
  } finally {
    if (currentSeq === usersTrendLoadSeq) {
      userTrendLoading.value = false
    }
  }
}

const loadUserSpendingRanking = async ({ silent = false }: LoadOptions = {}) => {
  const currentSeq = ++rankingLoadSeq
  if (!silent) {
    rankingLoading.value = true
    rankingError.value = false
  }
  try {
    const response = await adminAPI.dashboard.getUserSpendingRanking({
      start_date: startDate.value,
      end_date: endDate.value,
      limit: rankingLimit
    })
    if (currentSeq !== rankingLoadSeq) return
    rankingItems.value = response.ranking || []
    rankingTotalActualCost.value = response.total_actual_cost || 0
    rankingTotalRequests.value = response.total_requests || 0
    rankingTotalTokens.value = response.total_tokens || 0
    rankingError.value = false
  } catch (error) {
    if (currentSeq !== rankingLoadSeq) return
    console.error('Error loading user spending ranking:', error)
    if (!silent) {
      rankingItems.value = []
      rankingTotalActualCost.value = 0
      rankingTotalRequests.value = 0
      rankingTotalTokens.value = 0
      rankingError.value = true
    }
  } finally {
    if (currentSeq === rankingLoadSeq) {
      rankingLoading.value = false
    }
  }
}

const refreshAll = async (options: LoadOptions = {}) => {
  refreshing.value = true
  try {
    await Promise.all([
      loadDashboardSnapshot(true, options),
      loadUsersTrend(options),
      loadUserSpendingRanking(options)
    ])
  } finally {
    refreshing.value = false
    autoRefresh.resetCountdown()
  }
}

const loadChartData = async () => {
  await Promise.all([
    loadDashboardSnapshot(false),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

// snapshot 接口服务端缓存 30 秒，更短的刷新间隔拿不到新数据
const autoRefresh = useAutoRefresh({
  storageKey: 'admin-dashboard-auto-refresh',
  intervals: [30, 60, 120, 300] as const,
  defaultInterval: 60,
  onRefresh: () => refreshAll({ silent: true }),
  shouldPause: () => document.hidden || loading.value || refreshing.value
})

onMounted(() => {
  void refreshBatchImageAccess()
  void refreshAll()
  if (autoRefresh.enabled.value) {
    autoRefresh.start()
  }
})
</script>

<style scoped>
</style>
