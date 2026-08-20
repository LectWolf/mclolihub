<template>
  <section class="card overflow-hidden !rounded-lg !border-gray-200 p-0 dark:!border-dark-700">
    <header class="flex items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700 sm:px-5">
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('groupHealth.title') }}</h2>
        <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('groupHealth.window') }}</p>
      </div>
      <button class="btn btn-ghost btn-icon h-8 w-8" type="button" :title="t('common.refresh')" :disabled="loading" @click="reload">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
      </button>
    </header>

    <div v-if="loading && items.length === 0" class="space-y-2 p-4" aria-hidden="true">
      <div v-for="i in 3" :key="i" class="h-16 animate-pulse rounded-md bg-gray-100 dark:bg-dark-700" />
    </div>
    <div v-else-if="items.length === 0" class="px-5 py-8 text-center text-sm text-gray-400">{{ t('groupHealth.empty') }}</div>
    <div v-else class="overflow-x-auto">
      <table class="table min-w-[980px]">
        <thead>
          <tr>
            <th>{{ t('groupHealth.group') }}</th>
            <th><button type="button" class="inline-flex items-center gap-1" @click="toggleSort('rate_multiplier')">{{ t('groupHealth.multiplier') }} <span v-if="sortKey === 'rate_multiplier'">{{ sortOrder === 'asc' ? '↑' : '↓' }}</span></button></th>
            <th>{{ t('groupHealth.status') }}</th>
            <th><button type="button" class="inline-flex items-center gap-1" @click="toggleSort('real_ttft_p50_ms')">{{ t('groupHealth.realTtft') }} <span v-if="sortKey === 'real_ttft_p50_ms'">{{ sortOrder === 'asc' ? '↑' : '↓' }}</span></button></th>
            <th><button type="button" class="inline-flex items-center gap-1" @click="toggleSort('probe_ttft_ms')">{{ t('groupHealth.probeTtft') }} <span v-if="sortKey === 'probe_ttft_ms'">{{ sortOrder === 'asc' ? '↑' : '↓' }}</span></button></th>
            <th><button type="button" class="inline-flex items-center gap-1" @click="toggleSort('real_availability_6h')">{{ t('groupHealth.availability') }} <span v-if="sortKey === 'real_availability_6h'">{{ sortOrder === 'asc' ? '↑' : '↓' }}</span></button></th>
            <th class="w-[220px]">{{ t('groupHealth.trend') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in sortedItems" :key="item.group_id" :class="loading ? 'opacity-60' : ''">
            <td>
              <strong class="block max-w-[220px] truncate text-sm text-gray-900 dark:text-white">{{ item.name }}</strong>
              <span class="text-xs text-gray-400">{{ item.platform }}</span>
            </td>
            <td class="font-medium tabular-nums">{{ formatMultiplier(item.rate_multiplier) }}</td>
            <td>
              <span class="badge" :class="statusClass(item.status)">{{ statusLabel(item.status) }}</span>
              <span v-if="item.last_success_at" class="mt-1 block text-[11px] text-gray-400">{{ formatTime(item.last_success_at) }}</span>
            </td>
            <td>
              <span class="block font-medium tabular-nums">{{ formatMs(item.real_ttft_p50_ms) }}</span>
              <span class="block text-[11px] text-gray-400">
                {{ t('groupHealth.latencyDetail', { avg: formatMs(item.real_ttft_avg_ms), p95: formatMs(item.real_ttft_p95_ms), samples: item.real_ttft_samples }) }}
              </span>
            </td>
            <td>
              <span class="block font-medium tabular-nums">{{ formatMs(item.probe_ttft_ms) }}</span>
              <span class="block max-w-[180px] truncate text-[11px] text-gray-400" :title="item.probe_model">{{ item.probe_enabled ? item.probe_model : t('groupHealth.notEnabled') }}</span>
            </td>
            <td>
              <span class="block font-medium tabular-nums">{{ formatPercent(item.real_availability_6h) }}</span>
              <span class="block text-[11px] text-gray-400">{{ t('groupHealth.probeAvailability', { value: formatPercent(item.probe_availability_6h) }) }}</span>
            </td>
            <td>
              <div class="grid h-9 grid-flow-col grid-rows-1 gap-px" :style="{ gridTemplateColumns: `repeat(${trend(item).length}, minmax(3px, 1fr))` }" :aria-label="t('groupHealth.trend')">
                <span v-for="(bucket, index) in trend(item)" :key="index" class="relative h-full min-w-[3px] cursor-help rounded-[1px]" :title="bucketTitle(bucket)">
                  <span v-if="bucket?.real_ttft_ms" class="absolute inset-x-0 bottom-0 rounded-[1px] bg-violet-500/80" :style="{ height: `${barHeight(bucket.real_ttft_ms)}%` }" />
                  <span v-if="bucket?.probe_ttft_ms" class="absolute inset-x-0 bottom-0 rounded-[1px]" :class="probeBarClass(bucket.probe_ttft_ms)" :style="{ height: `${barHeight(bucket.probe_ttft_ms)}%` }" />
                </span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { listGroupHealth, type GroupHealthItem, type GroupHealthStatus, type GroupHealthTrendBucket } from '@/api/groupHealth'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatMultiplier as formatRateMultiplier } from '@/utils/formatters'

const { t, locale } = useI18n()
const appStore = useAppStore()
const items = ref<GroupHealthItem[]>([])
const loading = ref(false)
const sortKey = ref<'rate_multiplier' | 'real_ttft_p50_ms' | 'probe_ttft_ms' | 'real_availability_6h'>('rate_multiplier')
const sortOrder = ref<'asc' | 'desc'>('asc')
let controller: AbortController | null = null
let timer: number | null = null

async function reload() {
  controller?.abort()
  const next = new AbortController()
  controller = next
  loading.value = true
  try {
    const result = await listGroupHealth(next.signal)
    if (!next.signal.aborted) items.value = result.items || []
  } catch (error) {
    const e = error as { name?: string; code?: string }
    if (e?.name !== 'AbortError' && e?.name !== 'CanceledError' && e?.code !== 'ERR_CANCELED') {
      appStore.showError(extractApiErrorMessage(error, t('groupHealth.loadError')))
    }
  } finally {
    if (controller === next) loading.value = false
  }
}

function toggleSort(key: typeof sortKey.value) {
  if (sortKey.value === key) sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
  else { sortKey.value = key; sortOrder.value = 'asc' }
}
const sortedItems = computed(() => [...items.value].sort((a, b) => {
  const av = a[sortKey.value] as number; const bv = b[sortKey.value] as number
  const cmp = (av || 0) - (bv || 0)
  return (sortOrder.value === 'asc' ? cmp : -cmp) || a.name.localeCompare(b.name)
}))

function formatMs(value: number) {
  return value > 0 ? `${Math.round(value)} ms` : '-'
}
function formatPercent(value: number) {
  return Number.isFinite(value) && value >= 0 ? `${value.toFixed(value >= 99 ? 2 : 1)}%` : '-'
}
function formatMultiplier(value: number) {
  return `${formatRateMultiplier(Number(value || 0))}x`
}
function formatTime(value: string) {
  return new Intl.DateTimeFormat(locale.value || undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
}
function statusClass(status: GroupHealthStatus) {
  if (status === 'healthy') return 'badge-success'
  if (status === 'balance_insufficient') return 'badge-info'
  if (status === 'not_enabled') return 'badge-gray'
  return 'badge-danger'
}
function statusLabel(status: GroupHealthStatus) {
  return t(`groupHealth.statuses.${status}`, status === 'unavailable' ? '不可用' : status)
}

type DisplayBucket = GroupHealthTrendBucket | null
function trend(item: GroupHealthItem): DisplayBucket[] {
  const result: DisplayBucket[] = Array.from({ length: 72 }, () => null)
  const end = Date.now()
  const start = end - 12 * 60 * 60 * 1000
  for (const bucket of item.trend || []) {
    const index = Math.floor((new Date(bucket.started_at).getTime() - start) / (10 * 60 * 1000))
    if (index >= 0 && index < result.length) result[index] = bucket
  }
  return result
}
const maxTrendTTFT = computed(() => Math.max(1000, ...items.value.flatMap((item) => (item.trend || []).flatMap((bucket) => [bucket.probe_ttft_ms || 0, bucket.real_ttft_ms || 0]))))
function barHeight(value: number) { return Math.max(4, Math.min(100, (value / maxTrendTTFT.value) * 100)) }
function probeBarClass(value: number) { return value >= 8000 ? 'bg-red-500' : value >= 3000 ? 'bg-amber-400' : 'bg-emerald-500' }
function bucketTitle(bucket: DisplayBucket) {
  if (!bucket) return t('groupHealth.noData')
  return `${formatTime(bucket.started_at)} · ${t('groupHealth.bucketDetail', { success: bucket.real_success + bucket.probe_success, failure: bucket.real_failure + bucket.probe_failure })} · ${t('groupHealth.probeTtft')}: ${formatMs(bucket.probe_ttft_ms)} · ${t('groupHealth.realTtft')}: ${formatMs(bucket.real_ttft_ms)}`
}

onMounted(() => {
  void reload()
  timer = window.setInterval(() => void reload(), 30_000)
})
onBeforeUnmount(() => {
  controller?.abort()
  if (timer != null) window.clearInterval(timer)
})
</script>
