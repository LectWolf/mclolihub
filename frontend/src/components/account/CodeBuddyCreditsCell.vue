<template>
  <div v-if="visible" class="space-y-1" data-test="codebuddy-credits">
    <div
      v-if="snapshot"
      ref="segmentTriggerRef"
      class="space-y-1"
      @mouseenter="openSegmentCard"
      @mouseleave="scheduleCloseSegmentCard"
    >
      <div class="flex items-center gap-1">
        <span class="text-[11px] font-medium" :class="balanceClass">
          {{ t('admin.accounts.codebuddyOAuth.creditsRemaining', { count: formatCredits(snapshot.credits) }) }}
        </span>
        <span v-if="snapshot.exhausted" class="rounded bg-red-100 px-1 text-[9px] text-red-700 dark:bg-red-900/40 dark:text-red-300">
          {{ t('admin.accounts.codebuddyOAuth.creditsExhausted') }}
        </span>
        <span
          v-else-if="snapshot.estimated"
          class="rounded bg-amber-100 px-1 text-[9px] text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
        >
          {{ t('admin.accounts.codebuddyOAuth.creditsEstimated') }}
        </span>
      </div>

      <div v-if="totalCredits > 0" class="h-1 w-28 shrink-0 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
        <div class="h-full rounded-full transition-all" :class="barClass" :style="{ width: `${usedPercent}%` }" />
      </div>

      <div v-if="expiryLabel" class="text-[10px] text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.codebuddyOAuth.creditsExpiry', { time: expiryLabel }) }}
      </div>

      <Teleport to="body">
        <div
          v-if="showSegmentCard"
          class="fixed z-[9999] w-[17.5rem] rounded-lg border border-gray-200 bg-white p-2.5 shadow-xl dark:border-dark-600 dark:bg-dark-800"
          :style="segmentCardStyle"
          @mouseenter="openSegmentCard"
          @mouseleave="scheduleCloseSegmentCard"
        >
          <div v-if="expiryExact" class="mb-1.5 text-[10px] text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.codebuddyOAuth.creditsExpiry', { time: expiryExact }) }}
          </div>
          <div v-if="segments.length" class="mb-1.5 text-[10px] font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">
            {{ t('admin.accounts.codebuddyOAuth.creditsPackages') }}
          </div>
          <div
            v-if="segments.length"
            class="space-y-1.5 overflow-y-auto overscroll-contain [scrollbar-width:thin]"
            :style="segmentListStyle"
          >
            <div
              v-for="(segment, index) in segments"
              :key="`${segment.source || 'credits'}-${index}`"
              class="rounded-md px-2 py-1.5"
              :class="index === 0 ? 'bg-sky-50 dark:bg-sky-900/20' : 'bg-gray-50 dark:bg-dark-700/60'"
            >
              <div class="line-clamp-2 break-words text-[11px] font-medium leading-snug text-gray-800 dark:text-gray-200">
                {{ segmentLabel(segment) }}
              </div>
              <div class="mt-0.5 flex items-center justify-between gap-2 text-[10px] text-gray-500 dark:text-gray-400">
                <span>{{ formatCredits(segment.remaining) }} / {{ formatCredits(segment.total) }}</span>
                <span v-if="segmentExpiry(segment)">{{ segmentExpiry(segment) }}</span>
              </div>
            </div>
          </div>
          <div v-if="todayUsage" class="mt-1.5 text-[10px] text-gray-500 dark:text-gray-400">
            {{
              t('admin.accounts.codebuddyOAuth.creditsUsedToday', {
                credits: formatCredits(todayUsage.credits),
                requests: todayUsage.requests
              })
            }}
            <span v-if="!todayUsage.official" class="text-gray-400">
              {{ t('admin.accounts.codebuddyOAuth.creditsGatewayOnly') }}
            </span>
          </div>
          <div v-if="fetchedLabel" class="mt-1 text-[10px]" :class="stale ? 'text-amber-600 dark:text-amber-400' : 'text-gray-400'">
            {{ fetchedLabel }}
          </div>
        </div>
      </Teleport>
    </div>
    <div v-else-if="!loading" class="text-xs text-gray-400">-</div>

    <div class="flex flex-wrap items-center gap-1.5">
      <button
        type="button"
        class="inline-flex items-center gap-0.5 whitespace-nowrap rounded px-1.5 py-0.5 text-[10px] font-medium leading-4 text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
        :disabled="loading"
        @click="handleProbe"
      >
        <svg
          class="h-2.5 w-2.5"
          :class="{ 'animate-spin': loading }"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
          />
        </svg>
        {{ t('admin.accounts.codebuddyOAuth.probeCredits') }}
      </button>
    </div>

    <div v-if="error" class="truncate text-[10px] text-red-600 dark:text-red-400" :title="error">
      {{ error }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch, type CSSProperties } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type {
  CodeBuddyCreditSegment,
  CodeBuddyCreditsSnapshot,
  CodeBuddyCreditsUsage,
  CodeBuddyRequestUsage
} from '@/api/admin/codebuddy'
import type { Account } from '@/types'

/** Matches the backend probe TTL so both sides agree on what "stale" means. */
const SNAPSHOT_TTL_SECONDS = 10 * 60
const VISIBLE_PACK_COUNT = 5
const PACK_ROW_PX = 56
const CARD_CHROME_PX = 96
const SEGMENT_CLOSE_MS = 180
const props = defineProps<{ account: Account }>()
const { t } = useI18n()

const visible = computed(() => props.account.platform === 'codebuddy')
const loading = ref(false)
const error = ref('')
const snapshot = ref<CodeBuddyCreditsSnapshot | null>(null)
const usage = ref<CodeBuddyCreditsUsage | null>(null)
const official = ref<CodeBuddyRequestUsage | null>(null)

// The account row carries the last persisted probe and tally, so the cell shows
// a balance without an upstream round trip on every page load.
const loadFromExtra = () => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  const rawCredits = extra?.codebuddy_credits
  if (rawCredits && typeof rawCredits === 'object') {
    snapshot.value = rawCredits as CodeBuddyCreditsSnapshot
  }
  const rawUsage = extra?.codebuddy_credits_usage
  if (rawUsage && typeof rawUsage === 'object') {
    usage.value = rawUsage as CodeBuddyCreditsUsage
  }
  const rawOfficial = extra?.codebuddy_request_usage
  if (rawOfficial && typeof rawOfficial === 'object') {
    official.value = rawOfficial as CodeBuddyRequestUsage
  }
}

const formatCredits = (value: number | undefined) => {
  if (value == null || Number.isNaN(value)) return '0'
  return Number.isInteger(value) ? String(value) : value.toFixed(2)
}

const segments = computed<CodeBuddyCreditSegment[]>(() => snapshot.value?.segments || [])

// Segment totals only add up when the probe reported them, so the bar is hidden
// rather than guessed when the upstream omits capacities.
const totalCredits = computed(() =>
  segments.value.reduce((sum, segment) => sum + (segment.total || 0), 0)
)

const usedPercent = computed(() => {
  const total = totalCredits.value
  if (total <= 0) return 0
  const remaining = snapshot.value?.credits ?? 0
  return Math.min(100, Math.max(0, ((total - remaining) / total) * 100))
})

const remainingRatio = computed(() => {
  const total = totalCredits.value
  if (total <= 0) return 1
  return Math.max(0, (snapshot.value?.credits ?? 0) / total)
})

const balanceClass = computed(() => {
  if (snapshot.value?.exhausted || (snapshot.value?.credits ?? 0) <= 0) {
    return 'text-red-600 dark:text-red-400'
  }
  if (remainingRatio.value <= 0.1) return 'text-amber-600 dark:text-amber-400'
  return 'text-sky-700 dark:text-sky-300'
})

const barClass = computed(() => {
  if (remainingRatio.value <= 0.1) return 'bg-red-500'
  if (remainingRatio.value <= 0.3) return 'bg-amber-500'
  return 'bg-sky-500'
})

const segmentLabel = (segment: CodeBuddyCreditSegment) => {
  if (!segment.source || segment.source === 'credits') {
    return t('admin.accounts.codebuddyOAuth.creditsSegmentDefault')
  }
  return segment.source
}

const segmentTriggerRef = ref<HTMLElement | null>(null)
const showSegmentCard = ref(false)
const segmentCardStyle = ref<CSSProperties>({})
let closeTimer: ReturnType<typeof setTimeout> | null = null

const segmentListStyle = computed<CSSProperties>(() => {
  if (segments.value.length <= VISIBLE_PACK_COUNT) return {}
  return { maxHeight: `${VISIBLE_PACK_COUNT * PACK_ROW_PX}px` }
})

const cancelCloseSegmentCard = () => {
  if (closeTimer == null) return
  clearTimeout(closeTimer)
  closeTimer = null
}

const formatDateTime = (unixSeconds?: number | null) => {
  if (!unixSeconds) return ''
  const date = new Date(unixSeconds * 1000)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString()
}

const formatExpiryWithin = (unixSeconds?: number | null) => {
  if (!unixSeconds) return ''
  const date = new Date(unixSeconds * 1000)
  if (Number.isNaN(date.getTime())) return ''
  const diffMs = date.getTime() - Date.now()
  if (diffMs <= 0) return t('admin.accounts.codebuddyOAuth.creditsExpiryOverdue')
  const minutes = Math.max(1, Math.ceil(diffMs / 60_000))
  if (minutes < 60) {
    return t('admin.accounts.codebuddyOAuth.creditsExpiryWithinMinutes', { count: minutes })
  }
  const hours = Math.ceil(diffMs / 3_600_000)
  if (hours < 24) {
    return t('admin.accounts.codebuddyOAuth.creditsExpiryWithinHours', { count: hours })
  }
  return t('admin.accounts.codebuddyOAuth.creditsExpiryWithinDays', {
    count: Math.ceil(diffMs / 86_400_000)
  })
}

const segmentExpiry = (segment: CodeBuddyCreditSegment) => formatDateTime(segment.expires_at)

const openSegmentCard = () => {
  cancelCloseSegmentCard()
  const el = segmentTriggerRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const cardWidth = 280
  const gap = 6
  const estimatedHeight =
    CARD_CHROME_PX + Math.min(segments.value.length, VISIBLE_PACK_COUNT) * PACK_ROW_PX
  let left = rect.left
  if (left + cardWidth > window.innerWidth - 8) {
    left = Math.max(8, window.innerWidth - cardWidth - 8)
  }
  let top = rect.bottom + gap
  if (top + estimatedHeight > window.innerHeight - 8 && rect.top - gap - estimatedHeight > 8) {
    top = rect.top - gap - estimatedHeight
  }
  segmentCardStyle.value = {
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`
  }
  showSegmentCard.value = true
}

const scheduleCloseSegmentCard = () => {
  cancelCloseSegmentCard()
  closeTimer = setTimeout(() => {
    showSegmentCard.value = false
    closeTimer = null
  }, SEGMENT_CLOSE_MS)
}

const closeSegmentCard = () => {
  cancelCloseSegmentCard()
  showSegmentCard.value = false
}

const expiryLabel = computed(() => formatExpiryWithin(snapshot.value?.soonest_expiry))

const expiryExact = computed(() => formatDateTime(snapshot.value?.soonest_expiry))

const stale = computed(() => {
  const fetchedAt = snapshot.value?.fetched_at
  if (!fetchedAt) return true
  return Date.now() / 1000 - fetchedAt > SNAPSHOT_TTL_SECONDS
})

const fetchedLabel = computed(() => {
  const fetchedAt = snapshot.value?.fetched_at
  if (!fetchedAt) return ''
  const minutes = Math.floor((Date.now() / 1000 - fetchedAt) / 60)
  if (minutes < 1) return t('admin.accounts.codebuddyOAuth.creditsFetchedJustNow')
  if (minutes < 60) return t('admin.accounts.codebuddyOAuth.creditsFetchedMinutes', { minutes })
  return t('admin.accounts.codebuddyOAuth.creditsFetchedHours', { hours: Math.floor(minutes / 60) })
})

const localDay = () => {
  const now = new Date()
  const month = String(now.getMonth() + 1).padStart(2, '0')
  return `${now.getFullYear()}-${month}-${String(now.getDate()).padStart(2, '0')}`
}

/**
 * CodeBuddy's own report wins when present: it counts every client on the
 * account, while the gateway tally only sees traffic routed through here and
 * prices it from catalog multipliers.
 */
const todayUsage = computed(() => {
  const billed = official.value?.days?.find((entry) => entry.day === localDay())
  if (billed) {
    return { credits: billed.credits, requests: billed.requests, official: true }
  }
  if (!usage.value) return null
  return { credits: usage.value.credits, requests: usage.value.requests, official: false }
})

const handleProbe = async () => {
  loading.value = true
  error.value = ''
  try {
    const report = await adminAPI.codebuddy.queryCredits(props.account.id)
    snapshot.value = report
    usage.value = report.usage ?? null
    official.value = report.official ?? null
  } catch (err: any) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}

watch(
  () => props.account.id,
  () => {
    snapshot.value = null
    usage.value = null
    official.value = null
    error.value = ''
    closeSegmentCard()
    loadFromExtra()
  }
)

onMounted(() => {
  loadFromExtra()
})

onUnmounted(() => {
  cancelCloseSegmentCard()
})
</script>
