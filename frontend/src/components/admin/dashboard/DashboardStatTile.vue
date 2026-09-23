<template>
  <div class="card flex flex-col p-4">
    <div class="flex items-start justify-between gap-2">
      <div class="flex min-w-0 items-center gap-2">
        <span :class="['flex-shrink-0 rounded-lg p-1.5', iconClass]">
          <Icon :name="icon" size="sm" :stroke-width="2" />
        </span>
        <span class="truncate text-xs font-medium text-gray-500 dark:text-gray-400">{{ label }}</span>
        <HelpTooltip v-if="hint" :content="hint" width-class="w-60" />
      </div>
      <span
        v-if="delta"
        data-testid="stat-delta"
        :title="deltaHint"
        :aria-label="deltaHint"
        :class="[
          'inline-flex flex-shrink-0 items-center gap-0.5 rounded-full px-1.5 py-0.5 text-[11px] font-medium',
          deltaToneClass
        ]"
      >
        <Icon
          v-if="delta.ratio !== null && delta.direction !== 'flat'"
          :name="delta.direction === 'up' ? 'arrowUp' : 'arrowDown'"
          size="xs"
          :stroke-width="2.5"
        />
        {{ delta.ratio === null ? '—' : formatDeltaRatio(delta.ratio) }}
      </span>
    </div>
    <p
      class="mt-3 truncate text-2xl font-semibold text-gray-900 dark:text-white"
      :class="valueClass"
      :title="valueTitle || value"
      data-testid="stat-value"
    >
      {{ value }}<span v-if="unit" class="ml-1 text-sm font-normal text-gray-400 dark:text-gray-500">{{ unit }}</span>
    </p>
    <div class="mt-1 space-y-0.5 text-xs text-gray-500 dark:text-gray-400">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import { formatDeltaRatio, type MetricDelta } from './dashboardMetrics'

export type DashboardTileIcon = 'dollar' | 'trendingUp' | 'chart' | 'cube' | 'users' | 'server' | 'bolt'

const props = defineProps<{
  label: string
  icon: DashboardTileIcon
  iconClass: string
  value: string
  unit?: string
  valueTitle?: string
  valueClass?: string
  hint?: string
  delta?: MetricDelta | null
  deltaHint?: string
}>()

const deltaToneClass = computed(() => {
  switch (props.delta?.tone) {
    case 'positive':
      return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
    case 'negative':
      return 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
})
</script>
