<template>
  <div class="card flex flex-col p-4">
    <div class="flex items-start justify-between gap-2">
      <div class="flex min-w-0 items-center gap-2">
        <span class="flex-shrink-0 rounded-lg bg-purple-100 p-1.5 text-purple-600 dark:bg-purple-900/30 dark:text-purple-400">
          <Icon name="server" size="sm" :stroke-width="2" />
        </span>
        <span class="truncate text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('admin.dashboard.accountPool') }}
        </span>
        <HelpTooltip :content="t('admin.dashboard.accountPoolHint')" width-class="w-64" />
      </div>
      <router-link
        to="/admin/accounts"
        class="flex flex-shrink-0 items-center gap-0.5 text-xs text-gray-500 transition-colors hover:text-primary-600 dark:text-gray-400 dark:hover:text-primary-400"
      >
        {{ t('admin.dashboard.manageAccounts') }}
        <Icon name="chevronRight" size="xs" />
      </router-link>
    </div>

    <p class="mt-3 text-2xl font-semibold text-gray-900 dark:text-white" data-testid="account-available">
      {{ formatInteger(availableCount) }}
      <span class="text-sm font-normal text-gray-400 dark:text-gray-500">/ {{ formatInteger(total) }}</span>
    </p>
    <p class="text-xs text-gray-500 dark:text-gray-400">
      {{ health ? t('admin.dashboard.schedulableOfTotal') : t('admin.dashboard.activeOfTotal') }}
    </p>

    <template v-if="health && total > 0">
      <div
        class="mt-3 flex h-2 w-full gap-0.5 overflow-hidden rounded-full"
        role="img"
        :aria-label="barSummary"
        data-testid="account-health-bar"
      >
        <div
          v-for="segment in visibleSegments"
          :key="segment.key"
          :class="['h-full min-w-[4px]', segment.colorClass]"
          :style="{ flexGrow: segment.count, flexBasis: 0 }"
          :title="`${segment.label} ${segment.count}`"
        />
      </div>
      <div class="mt-3 flex flex-wrap gap-x-3 gap-y-1.5">
        <component
          :is="segment.status ? 'router-link' : 'span'"
          v-for="segment in legendSegments"
          :key="segment.key"
          :to="segment.status ? { path: '/admin/accounts', query: { status: segment.status } } : undefined"
          :data-testid="`account-health-${segment.key}`"
          class="inline-flex items-center gap-1.5 rounded text-xs text-gray-600 transition-colors dark:text-gray-300"
          :class="segment.status ? 'hover:text-gray-900 dark:hover:text-white' : ''"
        >
          <span :class="['h-2 w-2 flex-shrink-0 rounded-full', segment.colorClass]" />
          <span>{{ segment.label }}</span>
          <span class="font-semibold text-gray-900 dark:text-white">{{ formatInteger(segment.count) }}</span>
        </component>
      </div>
    </template>
    <p v-else-if="!health && errorFallback > 0" class="mt-2 text-xs text-red-600 dark:text-red-400">
      {{ t('admin.dashboard.errorAccountsFallback', { n: errorFallback }) }}
    </p>

    <p
      v-if="overloaded > 0"
      class="mt-2 flex items-center gap-1 text-xs text-amber-700 dark:text-amber-400"
      data-testid="account-overloaded"
    >
      <Icon name="exclamationTriangle" size="xs" :stroke-width="2" />
      {{ t('admin.dashboard.overloadedHint', { n: overloaded }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import type { AccountHealthStats } from '@/types'
import { ACCOUNT_HEALTH_ORDER, type AccountHealthKey } from './dashboardMetrics'

const props = defineProps<{
  health: AccountHealthStats | null
  total: number
  /** 旧版后端没有 account_health 时的回退展示 */
  normalFallback: number
  errorFallback: number
  overloaded: number
}>()

const { t } = useI18n()

// 状态色：可调度=good，限流/临时不可调度=warning，余额不足=serious，异常=critical，停用/暂停=中性灰。
const SEGMENT_COLORS: Record<AccountHealthKey, string> = {
  available: 'bg-emerald-500',
  rate_limited: 'bg-amber-500',
  temp_unschedulable: 'bg-amber-300',
  unschedulable: 'bg-slate-400',
  balance_insufficient: 'bg-orange-500',
  error: 'bg-red-500',
  inactive: 'bg-gray-300 dark:bg-gray-600',
  other: 'bg-gray-200 dark:bg-gray-700'
}

const segmentLabels = computed<Record<AccountHealthKey, string>>(() => ({
  available: t('admin.dashboard.accountHealth.available'),
  rate_limited: t('admin.dashboard.accountHealth.rateLimited'),
  temp_unschedulable: t('admin.dashboard.accountHealth.tempUnschedulable'),
  unschedulable: t('admin.dashboard.accountHealth.unschedulable'),
  balance_insufficient: t('admin.dashboard.accountHealth.balanceInsufficient'),
  error: t('admin.dashboard.accountHealth.error'),
  inactive: t('admin.dashboard.accountHealth.inactive'),
  other: t('admin.dashboard.accountHealth.other')
}))

const segments = computed(() => {
  const health = props.health
  if (!health) return []
  return ACCOUNT_HEALTH_ORDER.map(({ key, status }) => ({
    key,
    status,
    count: health[key],
    label: segmentLabels.value[key],
    colorClass: SEGMENT_COLORS[key]
  }))
})

const visibleSegments = computed(() => segments.value.filter((segment) => segment.count > 0))
// 可调度始终展示（即使为 0，也是最需要关注的信号），其余分类只在有账号时出现。
const legendSegments = computed(() =>
  segments.value.filter((segment) => segment.key === 'available' || segment.count > 0)
)

const availableCount = computed(() => props.health?.available ?? props.normalFallback)

const barSummary = computed(() =>
  visibleSegments.value.map((segment) => `${segment.label} ${segment.count}`).join(' · ')
)

const formatInteger = (value: number): string => (Number.isFinite(value) ? value : 0).toLocaleString()
</script>
