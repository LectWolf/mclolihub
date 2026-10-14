<template>
  <section class="mx-auto w-full max-w-6xl space-y-5 px-1 py-2 sm:px-2">
    <header
      class="page-header mb-0 flex flex-wrap items-center justify-between gap-3 rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
    >
      <div class="min-w-0">
        <h2 class="page-title flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
          <span class="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-blue-50 text-blue-500 dark:bg-blue-900/30 dark:text-blue-400">
            <Icon name="chart" size="sm" />
          </span>
          {{ t('channelMonitorV2.settings.title') }}
        </h2>
        <p class="page-description mt-1.5 text-xs text-gray-500 dark:text-gray-400">
          {{ t('channelMonitorV2.settings.description') }}
        </p>
      </div>
      <button
        type="button"
        class="btn btn-primary"
        :disabled="saving || !dirty"
        @click="save"
      >
        <Icon name="check" size="sm" />
        {{ t('channelMonitorV2.settings.save') }}
      </button>
    </header>

    <div
      v-if="!systemModeV2"
      class="rounded-2xl border border-amber-200 bg-amber-50/90 px-4 py-3 text-sm text-amber-900 dark:border-amber-800/50 dark:bg-amber-900/20 dark:text-amber-100"
      role="status"
    >
      {{
        t('channelMonitorV2.settings.modeBanner', {
          mode: systemModeLabel,
          modeV2: t('channelMonitorV2.settings.modeV2'),
        })
      }}
      <router-link class="ml-1 font-medium underline" to="/admin/settings">{{ t('admin.settings.tabs.features') }}</router-link>
    </div>

    <div
      v-if="loading"
      class="card flex min-h-[200px] items-center justify-center !rounded-3xl !border-0 text-sm text-gray-400 shadow-sm ring-1 ring-gray-900/5 dark:ring-dark-700"
    >
      <span class="animate-pulse">{{ t('channelMonitorV2.settings.loading') }}</span>
    </div>

    <template v-else-if="draft">
      <div class="card divide-y divide-gray-100 !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:divide-dark-700 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
          <div>
            <strong class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.settings.enableTitle') }}</strong>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('channelMonitorV2.settings.enableHint') }}
            </p>
          </div>
          <Toggle v-model="draft.enabled" />
        </div>
        <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
          <div>
            <strong class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.settings.refreshTitle') }}</strong>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV2.settings.refreshHint') }}</p>
          </div>
          <div class="tabs inline-flex w-auto" role="group" :aria-label="t('channelMonitorV2.settings.refreshAria')">
            <button
              type="button"
              class="tab"
              :class="draft.refresh_interval_seconds === 60 ? 'tab-active' : ''"
              @click="draft.refresh_interval_seconds = 60"
            >
              1 min
            </button>
            <button
              type="button"
              class="tab"
              :class="draft.refresh_interval_seconds === 300 ? 'tab-active' : ''"
              @click="draft.refresh_interval_seconds = 300"
            >
              5 min
            </button>
          </div>
        </div>
      </div>

      <div class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="card-header !py-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.settings.platformsTitle') }}</h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
            {{ t('channelMonitorV2.settings.platformsHint') }}
          </p>
          <div v-if="availablePlatforms.length" class="mt-3 flex flex-wrap items-center gap-2">
            <Select
              v-model="selectedPlatform"
              class="min-w-48"
              :options="availablePlatforms"
              :aria-label="t('channelMonitorV2.settings.selectPlatform')"
              :placeholder="t('channelMonitorV2.settings.selectPlatform')"
              :disabled="saving"
            />
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="saving || !selectedPlatform"
              @click="addPlatform"
            >
              {{ t('channelMonitorV2.settings.addPlatform') }}
            </button>
          </div>
        </div>
        <div class="divide-y divide-gray-100 dark:divide-dark-700">
          <div
            v-for="platform in draft.platforms"
            :key="platform.platform"
            class="grid grid-cols-1 items-center gap-3 px-5 py-3 sm:grid-cols-[auto_7rem_minmax(0,1fr)_auto]"
          >
            <Toggle v-model="platform.enabled" />
            <strong class="text-sm font-medium text-gray-900 dark:text-white">{{ platformLabel(platform.platform) }}</strong>
            <input
              class="input"
              :value="platform.models.join(', ')"
              type="text"
              :placeholder="t('channelMonitorV2.settings.modelsPlaceholder')"
              @change="setModels(platform, $event)"
            />
            <span
              class="badge justify-self-start sm:justify-self-end"
              :class="platform.models.length ? 'badge-gray' : 'badge badge-primary'"
            >
              {{ platform.models.length ? t('channelMonitorV2.settings.badgeOther') : t('channelMonitorV2.settings.badgeAllModels') }}
            </span>
          </div>
        </div>
      </div>

      <div class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="card-header flex flex-wrap items-center justify-between gap-2 !py-3">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.settings.groupsTitle') }}</h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{
                draft.group_ids.length
                  ? t('channelMonitorV2.settings.groupsSelected', { count: draft.group_ids.length })
                  : t('channelMonitorV2.settings.groupsAll')
              }}
            </p>
          </div>
          <button
            v-if="draft.group_ids.length"
            type="button"
            class="btn btn-ghost btn-sm"
            @click="draft.group_ids = []"
          >
            {{ t('channelMonitorV2.settings.groupsAll') }}
          </button>
        </div>
        <div class="max-h-[min(40vh,280px)] overflow-y-auto px-3 py-2 sm:px-4">
          <div class="grid grid-cols-1 gap-1 sm:grid-cols-2">
            <label
              v-for="group in groups"
              :key="group.id"
              class="flex cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 text-sm transition hover:bg-gray-50 dark:hover:bg-dark-800/60"
            >
              <input
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500/40"
                :checked="draft.group_ids.includes(group.id)"
                @change="toggleGroup(group.id)"
              />
              <span class="min-w-0 flex-1 truncate font-medium text-gray-800 dark:text-gray-100">{{ group.name }}</span>
              <small class="shrink-0 text-xs text-gray-400">{{ platformLabel(group.platform) }} · #{{ group.id }}</small>
            </label>
          </div>
          <p v-if="groups.length === 0" class="empty-state py-8 text-sm text-gray-400">{{ t('channelMonitorV2.settings.groupsEmpty') }}</p>
        </div>
      </div>

      <div class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="card-header !py-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.settings.qualityTitle') }}</h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV2.settings.qualityHint') }}</p>
        </div>
        <p v-if="qualityLoading" class="px-5 py-3 text-sm" role="status">{{ t('channelMonitorV2.settings.loading') }}</p>
        <div v-else-if="qualityLoadError" class="px-5 py-3" role="alert">
          <p class="text-sm text-red-600">{{ t('channelMonitorV2.settings.qualityLoadFailed') }}</p>
          <button type="button" class="btn btn-ghost btn-sm" @click="loadQuality">{{ t('channelMonitorV2.settings.qualityRetry') }}</button>
        </div>
        <div v-else class="divide-y divide-gray-100 px-5 dark:divide-dark-700">
          <div v-for="group in groups" :key="'quality-' + group.id" class="space-y-3 py-4" :data-quality-group="group.id">
            <template v-if="qualityDrafts[group.id]">
              <div class="flex flex-wrap items-center gap-3 text-sm">
                <strong class="min-w-0 flex-1 break-words">{{ group.name }}</strong>
                <small class="text-gray-500">{{ qualityStatusLabel(qualityOf(group.id)?.status) }}</small>
                <Toggle v-model="qualityDrafts[group.id].enabled" :disabled="qualityBusy[group.id]" :aria-label="t('channelMonitorV2.settings.qualityTitle') + ': ' + group.name" />
              </div>
              <div class="grid gap-3 sm:grid-cols-3">
                <label>
                  <span class="input-label">{{ t('channelMonitorV2.settings.qualitySpend') }}</span>
                  <input :value="qualityDrafts[group.id].spend" class="input" type="number" min="0.01" max="1000000" step="any" :disabled="qualityBusy[group.id]" :aria-invalid="Boolean(qualityErrors[group.id])" data-quality-spend @input="qualityDrafts[group.id].spend = ($event.target as HTMLInputElement).value" />
                </label>
                <label>
                  <span class="input-label">{{ t('channelMonitorV2.settings.qualityInterval') }}</span>
                  <input :value="qualityDrafts[group.id].interval" class="input" type="number" min="1" max="10080" step="1" :disabled="qualityBusy[group.id]" :aria-invalid="Boolean(qualityErrors[group.id])" data-quality-interval @input="qualityDrafts[group.id].interval = ($event.target as HTMLInputElement).value" />
                </label>
                <label>
                  <span class="input-label">{{ t('channelMonitorV2.settings.qualityModel') }}</span>
                  <input v-model="qualityDrafts[group.id].model" class="input" type="text" :placeholder="t('channelMonitorV2.settings.qualityDefaultModel')" :disabled="qualityBusy[group.id]" data-quality-model />
                </label>
              </div>
              <div class="flex flex-wrap items-center gap-3">
                <button type="button" class="btn btn-primary btn-sm" :disabled="qualityBusy[group.id] || !qualityDirty(group.id)" @click="saveQuality(group.id)" data-quality-save>
                  <Icon name="check" size="sm" />
                  {{ t(qualityBusy[group.id] ? 'channelMonitorV2.settings.qualitySaving' : 'channelMonitorV2.settings.qualitySave') }}
                </button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="!canRunQuality(group.id)" @click="startQualityRun(group.id)" data-quality-run>
                  {{ t(qualityRuns[group.id]?.status === 'running' ? 'channelMonitorV2.settings.qualityRunning' : 'channelMonitorV2.settings.qualityRun') }}
                </button>
                <p v-if="qualityErrors[group.id]" class="text-sm text-red-600" role="alert">{{ qualityErrors[group.id] }}</p>
              </div>
              <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('channelMonitorV2.settings.qualityRunHint') }}</p>
              <p v-if="qualityDirty(group.id)" class="text-xs text-amber-700 dark:text-amber-400" data-quality-run-hint>{{ t('channelMonitorV2.settings.qualityRunUnsaved') }}</p>
              <p v-else-if="!qualityOf(group.id)?.enabled" class="text-xs text-amber-700 dark:text-amber-400" data-quality-run-hint>{{ t('channelMonitorV2.settings.qualityRunDisabled') }}</p>
              <div v-if="qualityRuns[group.id]" class="space-y-1 text-sm" role="status" data-quality-run-result>
                <p>{{ t('channelMonitorV2.settings.qualityRunResult') }} · {{ t(`channelMonitorV2.settings.qualityRunStatus.${qualityRuns[group.id]?.status}`) }}</p>
                <p data-quality-run-source>{{ t(`channelMonitorV2.settings.qualityRunSource.${qualityRuns[group.id]?.source || 'manual'}`) }}</p>
                <p>{{ t('channelMonitorV2.settings.qualityRunProgress', { checked: qualityRuns[group.id]?.checked_accounts, total: qualityRuns[group.id]?.total_accounts }) }}</p>
                <p v-if="qualityRuns[group.id]?.status === 'failed'">{{ t('channelMonitorV2.settings.qualityRunFailed') }}</p>
              </div>
              <div v-if="qualityRunErrors[group.id]" class="text-sm text-red-600" role="alert" data-quality-run-error>
                <p>{{ qualityRunErrors[group.id] }}</p>
                <button type="button" class="btn btn-ghost btn-sm" :disabled="qualityRunPending[group.id]" @click="checkQualityRun(group.id)" data-quality-run-retry>{{ t('channelMonitorV2.settings.qualityRunCheck') }}</button>
              </div>
              <ChannelMonitorV3QualityPreview v-if="qualityRuns[group.id]?.latest_quality" :group-id="group.id" :enabled="qualityOf(group.id)?.enabled === true" :latest="qualityRuns[group.id]?.latest_quality" :quality-run="qualityRuns[group.id] ?? undefined" />
            </template>
          </div>
          <p v-if="groups.length === 0" class="empty-state py-8 text-sm text-gray-400">{{ t('channelMonitorV2.settings.groupsEmpty') }}</p>
          <details v-if="qualityDefaultPrompt" class="py-4 text-sm">
            <summary class="cursor-pointer font-medium">{{ t('channelMonitorV2.settings.qualityPrompt') }}</summary>
            <pre class="mt-3 max-h-72 overflow-y-auto whitespace-pre-wrap break-words font-sans text-xs leading-relaxed">{{ qualityDefaultPrompt }}</pre>
          </details>
        </div>
      </div>

      <div class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="card-header !py-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.settings.errorsTitle') }}</h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
            {{ t('channelMonitorV2.settings.errorsHint') }}
          </p>
        </div>
        <div class="max-h-[min(40vh,320px)] overflow-y-auto px-3 py-2 sm:px-4">
          <div class="grid grid-cols-1 gap-1 sm:grid-cols-2">
            <label
              v-for="category in errorCategories"
              :key="category"
              class="flex cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 text-sm transition hover:bg-gray-50 dark:hover:bg-dark-800/60"
            >
              <input
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500/40"
                :checked="isCategoryIgnored(category)"
                @change="toggleIgnoredCategory(category)"
              />
              <span class="min-w-0 flex-1 truncate font-medium text-gray-800 dark:text-gray-100">
                {{ categoryLabel(category) }}
              </span>
              <small class="shrink-0 font-mono text-[10px] text-gray-400">{{ category }}</small>
            </label>
          </div>
        </div>
        <div class="border-t border-gray-100 px-5 py-3 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400">
          {{
            t('channelMonitorV2.settings.ignoredSummary', {
              ignored: draft.ignored_error_categories?.length || 0,
              counted: countedErrorCategoryCount,
            })
          }}
        </div>
      </div>

      <div class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <div class="card-header !py-3">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.settings.healthTitle') }}</h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
            {{ t('channelMonitorV2.settings.healthHint') }}
          </p>
        </div>
        <div class="grid grid-cols-1 gap-4 px-5 py-4 sm:grid-cols-2 lg:grid-cols-4">
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.minimumSample') }}</span>
            <input v-model.number="draft.health_thresholds.minimum_sample" class="input" type="number" min="1" max="10000" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.warningError') }}</span>
            <input v-model.number="warningErrorPercent" class="input" type="number" min="0" max="100" step="0.1" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.criticalError') }}</span>
            <input v-model.number="criticalErrorPercent" class="input" type="number" min="0" max="100" step="0.1" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.targetTtft') }}</span>
            <input v-model.number="draft.health_thresholds.target_ttft_ms" class="input" type="number" min="1" step="100" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.warningTtft') }}</span>
            <input v-model.number="draft.health_thresholds.warning_ttft_ms" class="input" type="number" min="1" step="100" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.criticalTtft') }}</span>
            <input v-model.number="draft.health_thresholds.critical_ttft_ms" class="input" type="number" min="1" step="100" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.warningCache') }}</span>
            <input v-model.number="warningCachePercent" class="input" type="number" min="0" max="100" step="0.1" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('channelMonitorV2.settings.fields.criticalCache') }}</span>
            <input v-model.number="criticalCachePercent" class="input" type="number" min="0" max="100" step="0.1" />
          </label>
        </div>
      </div>

      <div class="space-y-2">
        <div class="rounded-2xl border border-primary-200 bg-primary-50/80 px-4 py-3 text-sm text-primary-900 dark:border-primary-800/50 dark:bg-primary-900/20 dark:text-primary-100">
          <template v-if="namedModelCount === 0">
            {{ t('channelMonitorV2.settings.namedModelsEmpty') }}
          </template>
          <template v-else>
            {{ t('channelMonitorV2.settings.namedModelsCount', { count: namedModelCount }) }}
          </template>
        </div>
        <div class="rounded-2xl border border-gray-200 bg-gray-50/80 px-4 py-3 text-xs text-gray-600 dark:border-dark-600 dark:bg-dark-800/50 dark:text-gray-300">
          <p class="font-medium text-gray-800 dark:text-gray-100">{{ t('channelMonitorV2.settings.userContractTitle') }}</p>
          <ul class="mt-1.5 list-disc space-y-0.5 pl-4">
            <li>{{ t('channelMonitorV2.settings.userContract.health') }}</li>
            <li>{{ t('channelMonitorV2.settings.userContract.trend') }}</li>
            <li>{{ t('channelMonitorV2.settings.userContract.latency') }}</li>
            <li>{{ t('channelMonitorV2.settings.userContract.models') }}</li>
          </ul>
        </div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import ChannelMonitorV3QualityPreview from '@/components/user/monitor/ChannelMonitorV3QualityPreview.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel as catalogPlatformLabel } from '@/utils/platformColors'
import { getChannelMonitorMode, isChannelMonitorV2Mode } from '@/utils/featureFlags'
import {
  getConfig,
  updateConfig,
  MONITOR_ERROR_CATEGORIES,
  type MonitorConfig,
} from '@/api/channelMonitorV2'
import { adminAPI } from '@/api/admin'
import { getQualityRun, listQuality, runQualityCheck, saveQualitySettings, type GroupQualityRun, type GroupQualityStatus } from '@/api/admin/qualityCheck'
import type { AdminGroup } from '@/types'

const { t, te } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const draft = ref<MonitorConfig | null>(null)
const original = ref('')
const groups = ref<AdminGroup[]>([])
const qualityByGroup = ref<Record<number, GroupQualityStatus>>({})
interface QualityDraft { enabled: boolean; spend: string; interval: string; model: string }
const qualityDrafts = ref<Record<number, QualityDraft>>({})
const qualityOriginal = ref<Record<number, string>>({})
const qualityBusy = ref<Record<number, boolean>>({})
const qualityErrors = ref<Record<number, string>>({})
const qualityLoading = ref(true)
const qualityLoadError = ref(false)
const qualityRuns = ref<Record<number, GroupQualityRun | null>>({})
const qualityRunKnown = ref<Record<number, boolean>>({})
const qualityRunExpected = ref<Record<number, boolean>>({})
const qualityRunPending = ref<Record<number, boolean>>({})
const qualityRunErrors = ref<Record<number, string>>({})
const qualityRunTimers = new Map<number, ReturnType<typeof setTimeout>>()
const qualityRequests = new Set<AbortController>()
let disposed = false
const qualityDefaultPrompt = computed(() => Object.values(qualityByGroup.value).find(row => row.default_prompt)?.default_prompt || '')
const selectedPlatform = ref('')
const availablePlatforms = computed(() =>
  CONCRETE_PLATFORM_OPTIONS.filter(
    (option) => !draft.value?.platforms.some((platform) => platform.platform === option.value)
  )
)

function addPlatform() {
  if (!draft.value || !availablePlatforms.value.some((option) => option.value === selectedPlatform.value)) return
  draft.value.platforms.push({ platform: selectedPlatform.value, enabled: true, models: [] })
  selectedPlatform.value = ''
}

const dirty = computed(() => (draft.value ? JSON.stringify(draft.value) !== original.value : false))
const namedModelCount = computed(
  () => draft.value?.platforms.filter((p) => p.enabled).reduce((sum, p) => sum + p.models.length, 0) || 0
)
const errorCategories = MONITOR_ERROR_CATEGORIES
const countedErrorCategoryCount = computed(
  () => errorCategories.length - (draft.value?.ignored_error_categories?.length || 0)
)
/** System settings mode must be v2 for aggregation to run; config remains editable for prep. */
const systemModeV2 = computed(() => isChannelMonitorV2Mode())
const systemModeLabel = computed(() => {
  if (!appStore.cachedPublicSettings?.channel_monitor_enabled) {
    return t('channelMonitorV2.settings.modeClosed')
  }
  return getChannelMonitorMode() === 'v1'
    ? t('channelMonitorV2.settings.modeV1')
    : t('channelMonitorV2.settings.modeV2')
})
const defaultThresholds = {
  minimum_sample: 50,
  warning_error_rate: 0.05,
  critical_error_rate: 0.20,
  target_ttft_ms: 3000,
  warning_ttft_ms: 3000,
  critical_ttft_ms: 10000,
  // Higher is better: below 85% watch, below 60% critical.
  warning_cache_rate: 0.85,
  critical_cache_rate: 0.60,
  error_weight: 0.60,
  ttft_weight: 0.20,
  cache_weight: 0.20,
}

/** Factory ignored categories (matches backend DefaultChannelMonitorV2IgnoredErrorCategories). */
const defaultIgnoredErrorCategories = [
  'authentication',
  'client_cancelled',
  'content_policy',
  'context_limit',
  'group_access',
  'model_unsupported',
  'not_found',
  'quota_or_balance',
] as const
function percentModel(key: 'warning_error_rate' | 'critical_error_rate' | 'warning_cache_rate' | 'critical_cache_rate') {
  return computed({
    get: () => ((draft.value?.health_thresholds?.[key] ?? defaultThresholds[key]) * 100),
    set: (value: number) => {
      if (!draft.value) return
      draft.value.health_thresholds[key] = Math.max(0, Math.min(100, Number(value) || 0)) / 100
    },
  })
}
const warningErrorPercent = percentModel('warning_error_rate')
const criticalErrorPercent = percentModel('critical_error_rate')
const warningCachePercent = percentModel('warning_cache_rate')
const criticalCachePercent = percentModel('critical_cache_rate')

function setModels(platform: MonitorConfig['platforms'][number], event: Event) {
  platform.models = [
    ...new Set(
      (event.target as HTMLInputElement).value
        .split(',')
        .map((v) => v.trim())
        .filter(Boolean)
    ),
  ].sort()
}

function toggleGroup(id: number) {
  if (!draft.value) return
  draft.value.group_ids = draft.value.group_ids.includes(id)
    ? draft.value.group_ids.filter((value) => value !== id)
    : [...draft.value.group_ids, id].sort((a, b) => a - b)
}

function isCategoryIgnored(category: string): boolean {
  return Boolean(draft.value?.ignored_error_categories?.includes(category))
}

function toggleIgnoredCategory(category: string) {
  if (!draft.value) return
  const current = new Set(draft.value.ignored_error_categories || [])
  if (current.has(category)) current.delete(category)
  else current.add(category)
  draft.value.ignored_error_categories = [...current].sort()
}

function qualityOf(id: number) {
  return qualityByGroup.value[id]
}
function qualityStatusLabel(status?: string) {
  const key = `channelMonitorV2.settings.qualityStatus.${status || 'unknown'}`
  return te(key) ? t(key) : status || ''
}
function qualityDraft(row?: GroupQualityStatus): QualityDraft {
  return { enabled: row?.enabled ?? false, spend: String(row?.min_spend_usd ?? 5), interval: String(row?.min_interval_minutes ?? 30), model: row?.model_id ?? '' }
}
function qualityDirty(id: number) {
  return JSON.stringify(qualityDrafts.value[id]) !== qualityOriginal.value[id]
}
function canRunQuality(id: number) {
  return qualityOf(id)?.enabled === true && !qualityDirty(id) && !qualityBusy.value[id]
    && qualityRunKnown.value[id] === true && !qualityRunPending.value[id]
    && qualityRuns.value[id]?.status !== 'running'
}
function qualityRequest() {
  const request = new AbortController()
  qualityRequests.add(request)
  return request
}
function clearQualityRunTimer(id: number) {
  clearTimeout(qualityRunTimers.get(id))
  qualityRunTimers.delete(id)
}
function scheduleQualityRun(id: number) {
  clearQualityRunTimer(id)
  if (disposed) return
  qualityRunTimers.set(id, setTimeout(() => {
    qualityRunTimers.delete(id)
    void checkQualityRun(id)
  }, 5000))
}
async function refreshQualityGroup(id: number, signal: AbortSignal) {
  try {
    const rows = await listQuality(signal)
    if (disposed || signal.aborted) return
    const row = rows.find(item => item.group_id === id)
    if (row) qualityByGroup.value[id] = row
  } catch {
    if (!disposed && !signal.aborted) qualityRunErrors.value[id] = t('channelMonitorV2.settings.qualityRunRefreshFailed')
  }
}
async function recoverQualityRun(id: number, signal: AbortSignal, conflict = false) {
  const run = await getQualityRun(id, signal)
  if (disposed || signal.aborted) return
  qualityRuns.value[id] = run
  if ((!run && qualityRunExpected.value[id]) || (conflict && run?.status !== 'running')) {
    qualityRunKnown.value[id] = false
    qualityRunErrors.value[id] = t('channelMonitorV2.settings.qualityRunBusyUnavailable')
    return
  }
  qualityRunKnown.value[id] = true
  qualityRunErrors.value[id] = ''
  qualityRunExpected.value[id] = run?.status === 'running'
  if (run?.status === 'running') scheduleQualityRun(id)
  else if (run) await refreshQualityGroup(id, signal)
}
async function checkQualityRun(id: number) {
  if (disposed || qualityRunPending.value[id]) return
  clearQualityRunTimer(id)
  const request = qualityRequest()
  qualityRunPending.value[id] = true
  qualityRunErrors.value[id] = ''
  try {
    await recoverQualityRun(id, request.signal)
  } catch {
    if (disposed || request.signal.aborted) return
    qualityRunKnown.value[id] = false
    qualityRunErrors.value[id] = t('channelMonitorV2.settings.qualityRunPollFailed')
  } finally {
    qualityRequests.delete(request)
    if (!disposed) qualityRunPending.value[id] = false
  }
}
async function startQualityRun(id: number) {
  if (!canRunQuality(id) || disposed) return
  const previous = qualityRuns.value[id] ?? null
  const request = qualityRequest()
  qualityRunPending.value[id] = true
  qualityRunKnown.value[id] = false
  qualityRunErrors.value[id] = ''
  qualityRunExpected.value[id] = true
  try {
    const run = await runQualityCheck(id, request.signal)
    if (disposed || request.signal.aborted) return
    if (!run?.run_id) throw new Error('Quality run acceptance unavailable')
    qualityRuns.value[id] = run
    qualityRunKnown.value[id] = true
    qualityRunExpected.value[id] = run.status === 'running'
    if (run.status === 'running') scheduleQualityRun(id)
    else await refreshQualityGroup(id, request.signal)
  } catch (error) {
    if (disposed || request.signal.aborted) return
    const apiError = error as { status?: number; response?: { status?: number } }
    const status = apiError?.status ?? apiError?.response?.status
    if (status === 400) {
      qualityRuns.value[id] = previous
      qualityRunKnown.value[id] = true
      qualityRunExpected.value[id] = false
      qualityRunErrors.value[id] = t('channelMonitorV2.settings.qualityRunUnavailable')
    } else if (status === 409) {
      try {
        await recoverQualityRun(id, request.signal, true)
      } catch {
        if (!disposed && !request.signal.aborted) {
          qualityRunErrors.value[id] = t('channelMonitorV2.settings.qualityRunPollFailed')
        }
      }
    } else {
      qualityRunErrors.value[id] = t('channelMonitorV2.settings.qualityRunSubmitFailed')
    }
  } finally {
    qualityRequests.delete(request)
    if (!disposed) qualityRunPending.value[id] = false
  }
}
async function loadQuality() {
  qualityLoading.value = true
  qualityLoadError.value = false
  const request = qualityRequest()
  try {
    const rows = await listQuality(request.signal)
    if (disposed || request.signal.aborted) return
    qualityByGroup.value = Object.fromEntries(rows.map(row => [row.group_id, row]))
    for (const group of groups.value) {
      if (qualityDrafts.value[group.id]) continue
      const value = qualityDraft(qualityByGroup.value[group.id])
      qualityDrafts.value[group.id] = value
      qualityOriginal.value[group.id] = JSON.stringify(value)
    }
    for (const group of groups.value) void checkQualityRun(group.id)
  } catch {
    if (!disposed && !request.signal.aborted) qualityLoadError.value = true
  } finally {
    qualityRequests.delete(request)
    if (!disposed) qualityLoading.value = false
  }
}
async function saveQuality(id: number) {
  const value = qualityDrafts.value[id]
  if (!value || qualityBusy.value[id]) return
  const spendText = String(value.spend).trim()
  const intervalText = String(value.interval).trim()
  const spend = Number(spendText)
  const interval = Number(intervalText)
  const decimal = /^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/
  if (!decimal.test(spendText) || !Number.isFinite(spend) || spend < 0.01 || spend > 1000000) {
    qualityErrors.value[id] = t('channelMonitorV2.settings.qualitySpendInvalid')
    return
  }
  if (!/^\d+$/.test(intervalText) || !Number.isSafeInteger(interval) || interval < 1 || interval > 10080) {
    qualityErrors.value[id] = t('channelMonitorV2.settings.qualityIntervalInvalid')
    return
  }
  qualityErrors.value[id] = ''
  qualityBusy.value[id] = true
  try {
    const status = await saveQualitySettings(id, { enabled: value.enabled, min_spend_usd: spend, min_interval_minutes: interval, model_id: value.model.trim() })
    if (disposed) return
    qualityByGroup.value[id] = status
    qualityDrafts.value[id] = qualityDraft(status)
    qualityOriginal.value[id] = JSON.stringify(qualityDrafts.value[id])
    appStore.showSuccess(t('channelMonitorV2.settings.saveSuccess'))
  } catch {
    if (!disposed) qualityErrors.value[id] = t('channelMonitorV2.settings.qualitySaveFailed')
  } finally {
    if (!disposed) qualityBusy.value[id] = false
  }
}
function categoryLabel(category: string) {
  const key = `channelMonitorV2.errorCategories.${category}`
  return te(key) ? t(key) : category
}

function platformLabel(value: string) {
  if (value === 'anthropic') return 'Claude'
  if (value === 'kiro') return 'Kiro'
  return catalogPlatformLabel(value)
}

function normalizeConfig(value: MonitorConfig): MonitorConfig {
  const ignored = value.ignored_error_categories
  return {
    ...value,
    health_thresholds: { ...defaultThresholds, ...(value.health_thresholds || {}) },
    // Preserve explicit empty arrays from the server (operator cleared all).
    ignored_error_categories: [
      ...(ignored == null ? [...defaultIgnoredErrorCategories] : ignored),
    ].sort(),
  }
}

async function load() {
  loading.value = true
  try {
    const [value, groupRows] = await Promise.all([
      getConfig(),
      adminAPI.groups.getAllIncludingInactive(),
    ])
    if (disposed) return
    const normalized = normalizeConfig(value)
    draft.value = structuredClone(normalized)
    groups.value = groupRows
    await loadQuality()
    original.value = JSON.stringify(normalized)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV2.settings.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value) return
  saving.value = true
  try {
    const payload = normalizeConfig(draft.value)
    const value = await updateConfig(payload)
    const normalized = normalizeConfig(value)
    draft.value = structuredClone(normalized)
    original.value = JSON.stringify(normalized)
    appStore.showSuccess(t('channelMonitorV2.settings.saveSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV2.settings.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(load)
onUnmounted(() => {
  disposed = true
  for (const timer of qualityRunTimers.values()) clearTimeout(timer)
  qualityRunTimers.clear()
  for (const request of qualityRequests) request.abort()
  qualityRequests.clear()
})
</script>
