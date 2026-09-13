<template>
  <div v-if="visible" class="space-y-1" data-test="codebuddy-credits">
    <div v-if="snapshot" class="space-y-0.5">
      <div class="text-[11px] font-medium text-sky-700 dark:text-sky-300">
        {{ t('admin.accounts.codebuddyOAuth.creditsRemaining', { count: snapshot.credits }) }}
      </div>
      <div
        v-if="expiryLabel"
        class="text-[10px] text-gray-500 dark:text-gray-400"
      >
        {{ t('admin.accounts.codebuddyOAuth.creditsExpiry', { time: expiryLabel }) }}
      </div>
      <div
        v-if="snapshot.segments?.length"
        class="text-[10px] text-gray-500 dark:text-gray-400"
      >
        {{ segmentSummary }}
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
import type { Account } from '@/types'

interface CreditSegment {
  remaining: number
  total: number
  expires_at?: number
  source?: string
}

interface CreditsSnapshot {
  credits: number
  count?: number
  segments?: CreditSegment[]
  soonest_expiry?: number
  intl?: boolean
  fetched_at?: number
}

const props = defineProps<{ account: Account }>()
const { t } = useI18n()

const visible = computed(() => props.account.platform === 'codebuddy')
const loading = ref(false)
const error = ref('')
const snapshot = ref<CreditsSnapshot | null>(null)

const loadFromExtra = () => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  const raw = extra?.codebuddy_credits
  if (raw && typeof raw === 'object') {
    snapshot.value = raw as CreditsSnapshot
  }
}

const expiryLabel = computed(() => {
  const exp = snapshot.value?.soonest_expiry
  if (!exp) return ''
  const date = new Date(exp * 1000)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString()
})

const segmentSummary = computed(() => {
  const segments = snapshot.value?.segments || []
  return segments
    .slice(0, 3)
    .map((segment) => `${segment.source || '积分'} ${segment.remaining}`)
    .join(' · ')
})

const handleProbe = async () => {
  loading.value = true
  error.value = ''
  try {
    snapshot.value = await adminAPI.codebuddy.queryCredits(props.account.id)
  } catch (err: any) {
    error.value = err.response?.data?.detail || err.response?.data?.message || err.message || t('common.error')
  } finally {
    loading.value = false
  }
}

watch(
  () => props.account.id,
  () => {
    snapshot.value = null
    error.value = ''
    loadFromExtra()
  }
)

onMounted(() => {
  loadFromExtra()
})
</script>
