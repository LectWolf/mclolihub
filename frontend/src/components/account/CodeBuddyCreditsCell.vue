<template>
  <div v-if="visible" class="space-y-1" data-test="codebuddy-credits">
    <div v-if="snapshot" class="space-y-1">
      <div class="flex items-center gap-1">
        <span
          class="text-[11px] font-medium"
          :class="balanceClass"
          :title="balanceTitle"
        >
          {{ t('admin.accounts.codebuddyOAuth.creditsRemaining', { count: formatCredits(snapshot.credits) }) }}
        </span>
        <span v-if="snapshot.exhausted" class="rounded bg-red-100 px-1 text-[9px] text-red-700 dark:bg-red-900/40 dark:text-red-300">
          {{ t('admin.accounts.codebuddyOAuth.creditsExhausted') }}
        </span>
        <span
          v-else-if="snapshot.estimated"
          class="rounded bg-amber-100 px-1 text-[9px] text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
          :title="t('admin.accounts.codebuddyOAuth.creditsEstimatedHint')"
        >
          {{ t('admin.accounts.codebuddyOAuth.creditsEstimated') }}
        </span>
      </div>

      <div v-if="totalCredits > 0" class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
        <div class="h-full rounded-full transition-all" :class="barClass" :style="{ width: `${usedPercent}%` }" />
      </div>

      <div v-if="expiryLabel" class="text-[10px] text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.codebuddyOAuth.creditsExpiry', { time: expiryLabel }) }}
      </div>

      <div
        v-if="segmentSummary"
        class="truncate text-[10px] text-gray-500 dark:text-gray-400"
        :title="segmentDetail"
      >
        {{ segmentSummary }}
      </div>

      <div v-if="todayUsage" class="text-[10px] text-gray-500 dark:text-gray-400" :title="usageTitle">
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

      <div v-if="fetchedLabel" class="text-[10px]" :class="stale ? 'text-amber-600 dark:text-amber-400' : 'text-gray-400'">
        {{ fetchedLabel }}
      </div>
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
import { computed, onMounted, ref, watch } from 'vue'
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

const balanceTitle = computed(() => {
  if (totalCredits.value <= 0) return ''
  return `${formatCredits(snapshot.value?.credits)} / ${formatCredits(totalCredits.value)}`
})

const segmentLabel = (segment: CodeBuddyCreditSegment) => {
  // The backend emits a locale-neutral placeholder when the package has no name.
  if (!segment.source || segment.source === 'credits') {
    return t('admin.accounts.codebuddyOAuth.creditsSegmentDefault')
  }
  return segment.source
}

const segmentSummary = computed(() =>
  segments.value
    .slice(0, 3)
    .map((segment) => `${segmentLabel(segment)} ${formatCredits(segment.remaining)}`)
    .join(' · ')
)

const segmentDetail = computed(() =>
  segments.value
    .map((segment) => {
      const expiry = segment.expires_at ? ` → ${new Date(segment.expires_at * 1000).toLocaleString()}` : ''
      return `${segmentLabel(segment)} ${formatCredits(segment.remaining)}/${formatCredits(segment.total)}${expiry}`
    })
    .join('\n')
)

const expiryLabel = computed(() => {
  const expiry = snapshot.value?.soonest_expiry
  if (!expiry) return ''
  const date = new Date(expiry * 1000)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString()
})

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

const usageTitle = computed(() => {
  const parts: string[] = []
  if (official.value) {
    parts.push(
      t('admin.accounts.codebuddyOAuth.creditsOfficialWindow', {
        days: official.value.range_days,
        credits: formatCredits(official.value.total_credits),
        requests: official.value.requests
      })
    )
  }
  if (usage.value) {
    parts.push(
      t('admin.accounts.codebuddyOAuth.creditsUsedTotal', {
        credits: formatCredits(usage.value.total_credits),
        requests: usage.value.total_requests
      })
    )
    if (usage.value.unpriced) {
      parts.push(t('admin.accounts.codebuddyOAuth.creditsUnpriced', { count: usage.value.unpriced }))
    }
  }
  return parts.join('\n')
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
    loadFromExtra()
  }
)

onMounted(() => {
  loadFromExtra()
})
</script>
