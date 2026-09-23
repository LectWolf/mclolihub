<template>
  <div class="card p-4">
    <div class="mb-4 flex items-center gap-1">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('admin.dashboard.spendTrend') }}
      </h3>
      <HelpTooltip :content="t('admin.dashboard.spendTrendHint')" width-class="w-64" />
    </div>
    <div v-if="loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <div v-else-if="chartData" class="h-48">
      <Line :data="chartData" :options="lineOptions" />
    </div>
    <div v-else class="flex h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend
} from 'chart.js'
import { Line } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import type { TrendDataPoint } from '@/types'
import { trendAxisTicks } from '@/components/charts/trendAxis'
import { formatPercent, formatUsd, formatUsdAxis, profitMargin, toFiniteNumber } from './dashboardMetrics'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend)

const props = defineProps<{
  trendData: TrendDataPoint[]
  loading?: boolean
}>()

const { t } = useI18n()

const isDarkMode = computed(() => document.documentElement.classList.contains('dark'))

// 配色经 dataviz 校验（亮/暗两种卡片底色下均通过亮度、色度、色盲区分与对比度检查），
// 并沿用本站「实际=绿、成本=橙」的语义。
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb',
  actual: '#059669',
  account: '#ea580c',
  profit: isDarkMode.value ? '#3b82f6' : '#2563eb'
}))

// 旧版后端的趋势点没有 account_cost，此时只画实际消费。
const hasAccountCost = computed(() => props.trendData.some((point) => point.account_cost !== undefined))

const lineDataset = (label: string, data: number[], color: string) => ({
  label,
  data,
  borderColor: color,
  backgroundColor: color,
  borderWidth: 2,
  pointRadius: 0,
  pointHoverRadius: 4,
  pointHitRadius: 12,
  fill: false,
  tension: 0.3
})

const chartData = computed(() => {
  if (!props.trendData?.length) return null
  const colors = chartColors.value
  const actual = props.trendData.map((point) => toFiniteNumber(point.actual_cost))
  const datasets = [lineDataset(t('admin.dashboard.seriesActualCost'), actual, colors.actual)]
  if (hasAccountCost.value) {
    const account = props.trendData.map((point) => toFiniteNumber(point.account_cost))
    datasets.push(lineDataset(t('admin.dashboard.seriesAccountCost'), account, colors.account))
    datasets.push(
      lineDataset(
        t('admin.dashboard.seriesProfit'),
        actual.map((value, index) => value - account[index]),
        colors.profit
      )
    )
  }
  return {
    labels: props.trendData.map((point) => point.date),
    datasets
  }
})

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
        pointStyle: 'line',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      callbacks: {
        label: (context: any) => `${context.dataset.label}: ${formatUsd(context.raw)}`,
        footer: (items: any[]) => {
          const point = props.trendData[items[0]?.dataIndex]
          if (!point || !hasAccountCost.value) return ''
          return `${t('admin.dashboard.marginLabel')}: ${formatPercent(profitMargin(point.actual_cost, point.account_cost))}`
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
        callback: (value: string | number) => formatUsdAxis(value)
      }
    }
  }
}))
</script>
