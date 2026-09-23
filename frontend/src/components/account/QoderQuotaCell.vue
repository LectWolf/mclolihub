<template>
  <div v-if="visible" class="space-y-1" data-test="qoder-quota">
    <template v-if="snapshot">
      <!-- 套餐名显示在「平台/类型」列（与 Grok / OpenAI 一致），这里只展示额度本身。 -->
      <div class="flex flex-wrap items-center gap-1">
        <span
          v-if="snapshot.exceeded"
          class="rounded bg-red-100 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-900/40 dark:text-red-300"
          data-test="qoder-quota-exceeded"
        >
          {{ t('admin.accounts.qoderQuota.exceeded') }}
        </span>
        <span
          v-if="pools.length"
          class="text-[11px] font-medium tabular-nums"
          :class="remainingClass"
          :title="t('admin.accounts.qoderQuota.remainingTitle')"
        >
          {{ t('admin.accounts.qoderQuota.remaining', { value: formatCredits(totalRemaining), unit: unitLabel }) }}
        </span>
      </div>

      <div
        v-for="pool in visiblePools"
        :key="`${pool.kind}-${pool.name || ''}`"
        :title="poolTitle(pool)"
        :class="{ 'opacity-50': !pool.available }"
      >
        <UsageProgressBar
          :label="poolLabel(pool)"
          :label-width="pool.kind === 'package' ? 'auto' : 'fixed'"
          :utilization="usedPercent(pool)"
          :resets-at="poolResetsAt(pool)"
          :color="poolColor(pool)"
        />
      </div>
      <div
        v-if="hiddenPools.length"
        class="text-[10px] text-gray-400"
        :title="hiddenPools.map(poolTitle).join('\n')"
      >
        {{ t('admin.accounts.qoderQuota.morePools', { count: hiddenPools.length }) }}
      </div>
      <div v-if="!pools.length" class="text-[11px] text-gray-400">
        {{ t('admin.accounts.qoderQuota.noPools') }}
      </div>
    </template>
    <div v-else-if="!loading" class="text-xs text-gray-400">{{ t('admin.accounts.qoderQuota.never') }}</div>

    <div class="flex flex-wrap items-center gap-1.5">
      <button
        type="button"
        class="inline-flex items-center gap-0.5 whitespace-nowrap rounded px-1.5 py-0.5 text-[10px] font-medium leading-4 text-emerald-700 transition-colors hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-emerald-300 dark:hover:bg-emerald-900/30"
        :disabled="loading"
        data-test="qoder-quota-refresh"
        @click="refresh"
      >
        <svg class="h-2.5 w-2.5" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
          />
        </svg>
        {{ loading ? t('admin.accounts.qoderQuota.refreshing') : t('admin.accounts.qoderQuota.refresh') }}
      </button>
      <span
        v-if="checkedLabel"
        class="text-[10px]"
        :class="stale ? 'text-amber-600 dark:text-amber-400' : 'text-gray-400'"
        :title="checkedTitle"
      >
        {{ checkedLabel }}
      </span>
    </div>

    <div v-if="error" class="line-clamp-2 break-words text-[10px] text-red-600 dark:text-red-400" :title="error">
      {{ error }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { QoderQuotaPool, QoderQuotaSnapshot } from '@/api/admin/accounts'
import type { Account } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTimeToMinute, formatRelativeTime } from '@/utils/format'
import UsageProgressBar from './UsageProgressBar.vue'

/** Matches the backend reuse window of a stored snapshot. */
const SNAPSHOT_TTL_MS = 5 * 60 * 1000
/** Pools shown as bars; the rest collapse into a "+N" line. */
const MAX_VISIBLE_POOLS = 3

const props = defineProps<{ account: Account }>()
const { t } = useI18n()

const visible = computed(() => props.account.platform === 'qoder')
const loading = ref(false)
const error = ref('')
const fetched = ref<QoderQuotaSnapshot | null>(null)

// The account row carries the last stored snapshot, so the list shows credits
// without calling Qoder on every page load.
const storedSnapshot = computed<QoderQuotaSnapshot | null>(() => {
  const raw = (props.account.extra as Record<string, unknown> | undefined)?.qoder_quota
  if (!raw || typeof raw !== 'object') return null
  const snapshot = raw as QoderQuotaSnapshot
  return Array.isArray(snapshot.pools) ? snapshot : null
})

const snapshot = computed(() => fetched.value ?? storedSnapshot.value)

watch(
  () => props.account.id,
  () => {
    fetched.value = null
    error.value = ''
  }
)

const pools = computed(() => snapshot.value?.pools ?? [])
const visiblePools = computed(() => pools.value.slice(0, MAX_VISIBLE_POOLS))
const hiddenPools = computed(() => pools.value.slice(MAX_VISIBLE_POOLS))

const unitLabel = computed(() => {
  const unit = (snapshot.value?.unit || 'credits').trim()
  return unit.toLowerCase() === 'credits' ? t('admin.accounts.qoderQuota.credits') : unit
})

const totalRemaining = computed(() =>
  pools.value.filter((pool) => pool.available).reduce((sum, pool) => sum + Math.max(0, pool.remaining || 0), 0)
)

const remainingClass = computed(() => {
  const total = pools.value.filter((pool) => pool.available).reduce((sum, pool) => sum + (pool.total || 0), 0)
  if (snapshot.value?.exceeded || totalRemaining.value <= 0) return 'text-red-600 dark:text-red-400'
  if (total > 0 && totalRemaining.value / total <= 0.1) return 'text-amber-600 dark:text-amber-400'
  return 'text-gray-700 dark:text-gray-300'
})

const formatCredits = (value: number | undefined) => {
  const n = Number(value) || 0
  return n.toLocaleString(undefined, { maximumFractionDigits: Number.isInteger(n) ? 0 : 1 })
}

const usedPercent = (pool: QoderQuotaPool) => {
  if (!pool.total || pool.total <= 0) return 0
  return Math.min(100, Math.max(0, (pool.used / pool.total) * 100))
}

const poolKindLabel = (pool: QoderQuotaPool) => {
  switch (pool.kind) {
    case 'plan':
      return t('admin.accounts.qoderQuota.pools.plan')
    case 'addon':
      return t('admin.accounts.qoderQuota.pools.addon')
    case 'org':
      return t('admin.accounts.qoderQuota.pools.org')
    default:
      return t('admin.accounts.qoderQuota.pools.package')
  }
}

const poolLabel = (pool: QoderQuotaPool) => (pool.kind === 'package' && pool.name ? pool.name : poolKindLabel(pool))

const poolColor = (pool: QoderQuotaPool): 'indigo' | 'emerald' | 'purple' | 'amber' => {
  switch (pool.kind) {
    case 'plan':
      return 'emerald'
    case 'addon':
      return 'amber'
    case 'org':
      return 'purple'
    default:
      return 'indigo'
  }
}

// The top-level reset time belongs to the plan credits only.
const poolResetsAt = (pool: QoderQuotaPool) =>
  pool.expires_at || (pool.kind === 'plan' ? snapshot.value?.resets_at : undefined) || null

const poolTitle = (pool: QoderQuotaPool) => {
  const lines = [
    `${pool.kind === 'package' && pool.name ? pool.name : poolKindLabel(pool)}: ${t('admin.accounts.qoderQuota.poolDetail', {
      used: formatCredits(pool.used),
      total: formatCredits(pool.total),
      remaining: formatCredits(pool.remaining),
      unit: unitLabel.value
    })}`
  ]
  const resetsAt = poolResetsAt(pool)
  if (resetsAt) {
    lines.push(
      t(pool.kind === 'plan' ? 'admin.accounts.qoderQuota.resetsAt' : 'admin.accounts.qoderQuota.expiresAt', {
        time: formatDateTimeToMinute(resetsAt)
      })
    )
  }
  if (!pool.available) lines.push(t('admin.accounts.qoderQuota.unavailable'))
  return lines.join('\n')
}

const stale = computed(() => {
  const checkedAt = snapshot.value?.checked_at
  if (!checkedAt) return true
  const time = new Date(checkedAt).getTime()
  return Number.isNaN(time) || Date.now() - time > SNAPSHOT_TTL_MS
})

const checkedLabel = computed(() => {
  const checkedAt = snapshot.value?.checked_at
  return checkedAt ? t('admin.accounts.qoderQuota.checkedAt', { time: formatRelativeTime(checkedAt) }) : ''
})

const checkedTitle = computed(() => {
  const current = snapshot.value
  if (!current?.checked_at) return ''
  const credential =
    current.credential === 'pat'
      ? t('admin.accounts.qoderQuota.credential.pat')
      : t('admin.accounts.qoderQuota.credential.device')
  return `${formatDateTimeToMinute(current.checked_at)} · ${credential}`
})

const refresh = async () => {
  loading.value = true
  error.value = ''
  try {
    fetched.value = await adminAPI.accounts.getQoderQuota(props.account.id, true)
  } catch (err: unknown) {
    error.value = extractApiErrorMessage(err, t('admin.accounts.qoderQuota.failed'))
  } finally {
    loading.value = false
  }
}
</script>
