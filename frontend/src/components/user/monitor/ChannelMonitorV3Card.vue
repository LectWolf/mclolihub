<template>
  <article class="group relative z-0 glass-card flex min-h-[286px] flex-col overflow-visible rounded-[24px] p-5 text-left hover:z-20">
    <header class="flex items-start gap-3">
      <span class="grid h-9 w-9 shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <ChannelMonitorV3HoverTip v-if="groupDescription" class="block min-w-0">
          <div class="cursor-help truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ groupLabel }}</div>
          <template #tip>
            <div class="text-[11px] font-semibold">{{ groupLabel }}</div>
            <div class="mt-1 font-medium leading-relaxed text-slate-200">{{ groupDescription }}</div>
          </template>
        </ChannelMonitorV3HoverTip>
        <div v-else class="truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ groupLabel }}</div>
        <div class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <span class="rounded-md bg-primary-50 px-1.5 py-0.5 font-mono text-[10px] font-medium text-primary-700 dark:bg-dark-700 dark:text-gray-300">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</span>
          <span
            v-if="probeEnabled"
            class="inline-flex items-center gap-1 rounded-md bg-sky-50 px-1.5 py-0.5 text-[10px] font-medium text-sky-700 dark:bg-sky-500/15 dark:text-sky-300"
            :title="t('channelMonitorV3.probingHint')"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-sky-500" />
            {{ t('channelMonitorV3.probingBadge') }}
          </span>
        </div>
      </div>
      <span class="shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold" :class="statusClass">{{ statusText }}</span>
    </header>

    <div class="mt-5 grid grid-cols-3 gap-2">
      <div class="rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.cacheRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ cacheRate }}</div>
      </div>
      <div class="rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.successRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums" :class="availabilityClass">{{ successRate }}</div>
      </div>
      <div class="rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.ttft') }}</div>
        <div
          class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100"
          :title="ttftTitle"
        >{{ ttft }}</div>
      </div>
    </div>

    <ChannelMonitorV3Timeline
      class="mt-auto"
      :buckets="row.buckets"
      :probe-buckets="probeBuckets"
      :slot-bucket-ms="timelineBucketMs"
      :countdown-seconds="countdownSeconds"
      :length="timelineLength"
    />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { availabilityTextClass, formatMonitorMs, formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'
import {
  buildV3TimelineSlots,
  latestActiveV3Slot,
  resolveV3CardStatus,
  resolveV3CardTtftMs,
  statusFromAvailabilityRate,
  type V3CardGroupMeta,
} from '@/features/channel-monitor-v2/v3CardPresentation'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import { formatMultiplier } from '@/utils/formatters'
import ProviderIcon from './ProviderIcon.vue'
import ChannelMonitorV3HoverTip from './ChannelMonitorV3HoverTip.vue'
import ChannelMonitorV3Timeline from './ChannelMonitorV3Timeline.vue'

const props = defineProps<{
  row: MonitorMatrixRow
  countdownSeconds: number
  timelineLength: number
  userRateMultiplier?: number | null
  groupMeta?: V3CardGroupMeta | null
  timelineBucketMs: number
}>()
const { t } = useI18n()
const { statusLabel, statusBadgeClass, providerLabel, providerBadgeClass } = useChannelMonitorFormat()

const groupLabel = computed(() => props.row.group_name || t('channelMonitorV3.unknownGroup'))
const groupDescription = computed(() => {
  const value = props.groupMeta?.description?.trim()
  return value || undefined
})
const probeEnabled = computed(() => Boolean(props.groupMeta?.probeEnabled))
const formattedUserRate = computed(() => {
  const value = props.userRateMultiplier
  if (typeof value !== 'number' || !Number.isFinite(value)) return '-'
  if (value === 0) return t('channelMonitorV3.freeRate')
  return `${formatMultiplier(value)}x`
})
const probeBuckets = computed(() => (props.row.probe_buckets ?? [])
  .map(bucket => ({
    startMs: Date.parse(bucket.bucket_start),
    durationMs: 10 * 60 * 1000,
    success: bucket.success,
    failure: bucket.failure,
    ttftMs: bucket.ttft_ms,
  }))
  .filter(bucket => Number.isFinite(bucket.startMs)))
const latestBucket = computed(() => [...props.row.buckets]
  .filter(bucket => bucket.bucket_start && bucket.metrics)
  .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
  .at(-1))
const latestMetrics = computed(() => latestBucket.value?.metrics ?? props.row.metrics)
const latestHealth = computed(() => latestBucket.value?.health ?? props.row.health)
const timelineSlots = computed(() => buildV3TimelineSlots({
  nowMs: Date.now(),
  length: props.timelineLength,
  bucketMs: props.timelineBucketMs,
  trafficBuckets: (props.row.buckets ?? []).map(bucket => ({
    startMs: Date.parse(bucket.bucket_start),
    requestCount: bucket.metrics.request_count,
    errorRate: bucket.metrics.error_rate,
    cacheRate: bucket.metrics.cache_rate,
    ttftP50Ms: bucket.metrics.ttft.p50_ms,
    overall: bucket.health.overall,
  })).filter(bucket => Number.isFinite(bucket.startMs)),
  probeBuckets: probeBuckets.value,
}))
const latestSlot = computed(() => latestActiveV3Slot(timelineSlots.value))
// Cache stays traffic-only. Availability mixes overlapping probe samples into the latest slot.
const cacheRate = computed(() => formatMonitorPercent(latestMetrics.value.cache_rate))
const availabilityPercent = computed(() => {
  const mixed = latestSlot.value?.availabilityRate
  if (mixed != null && Number.isFinite(mixed)) return mixed * 100
  return (1 - latestMetrics.value.error_rate) * 100
})
const successRate = computed(() => formatMonitorPercent(availabilityPercent.value / 100))
const availabilityClass = computed(() => availabilityTextClass(availabilityPercent.value))
const resolvedStatus = computed(() => {
  const slot = latestSlot.value
  if (slot && (slot.source === 'probe' || slot.source === 'mixed')) {
    const fromRate = statusFromAvailabilityRate(slot.availabilityRate)
    if (fromRate) return { status: fromRate, source: slot.source }
  }
  const latestProbe = probeBuckets.value.at(-1)
  return resolveV3CardStatus({
    trafficOverall: latestHealth.value.overall,
    probeEnabled: probeEnabled.value,
    probeStatus: latestProbe
      ? (latestProbe.success > 0 ? 'healthy' : 'unavailable')
      : props.groupMeta?.probeStatus,
  })
})
const resolvedTtft = computed(() => resolveV3CardTtftMs({
  trafficTtftMs: latestMetrics.value.ttft.p50_ms,
  probeEnabled: probeEnabled.value,
  probeTtftMs: probeBuckets.value.at(-1)?.ttftMs ?? props.groupMeta?.probeTtftMs,
}))
const ttft = computed(() => formatMonitorMs(resolvedTtft.value.ms))
const ttftTitle = computed(() => resolvedTtft.value.source === 'probe' ? t('channelMonitorV3.probeTtftHint') : undefined)
const statusText = computed(() => {
  if (resolvedStatus.value.status === 'balance_insufficient') return t('groupHealth.statuses.balance_insufficient')
  if (resolvedStatus.value.status) return statusLabel(resolvedStatus.value.status)
  return t('channelMonitorV3.unknown')
})
const statusClass = computed(() => {
  if (resolvedStatus.value.status === 'balance_insufficient') {
    return 'bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300'
  }
  if (resolvedStatus.value.status) return statusBadgeClass(resolvedStatus.value.status)
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
})
</script>
