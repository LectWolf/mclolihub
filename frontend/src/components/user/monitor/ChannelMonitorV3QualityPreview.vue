<template>
  <div v-if="visible" ref="anchor" class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700" @focusout="onFocusOut">
    <p class="text-xs font-medium text-gray-700 dark:text-gray-200">{{ heading }}</p>
    <ul v-if="displayItems.length" class="mt-2 flex list-none flex-wrap gap-1.5 p-0">
      <li v-for="item in displayItems" :key="item.result_id">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-full border bg-white px-2 py-0.5 text-[11px] font-medium tabular-nums text-gray-700 hover:border-gray-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:bg-dark-800 dark:text-gray-200 dark:hover:border-dark-500"
          :class="open && activeId === item.result_id ? 'border-primary-400 ring-1 ring-primary-400' : 'border-gray-200 dark:border-dark-600'"
          :aria-expanded="open && activeId === item.result_id"
          :aria-controls="panelId"
          aria-haspopup="dialog"
          :aria-label="chipLabel(item)"
          @mouseenter="show(item, $event)"
          @mouseleave="leave"
          @focus="show(item, $event)"
          @click="togglePinned(item, $event)"
          @keydown.esc.prevent="close"
        >
          <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="dotClass(item)" aria-hidden="true" />
          <time :datetime="item.checked_at">{{ formatHm(item.checked_at) }}</time>
        </button>
      </li>
    </ul>
    <p v-else-if="historyLoading" class="mt-2 text-xs text-gray-500 dark:text-gray-400" role="status">{{ t('channelMonitorV3.quality.loading') }}</p>
    <p v-else class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.status.pending') }}</p>
    <Teleport to="body">
      <div
        v-if="open && activeItem"
        :id="panelId"
        ref="panel"
        role="dialog"
        :aria-label="`${verdictLabel(activeItem)} ${scoreText(activeItem)}`"
        class="quality-preview-panel fixed z-[60] overflow-auto overscroll-contain rounded-lg border border-gray-200 bg-white p-3 text-xs text-gray-800 shadow-lg dark:border-dark-600 dark:bg-dark-800 dark:text-gray-100"
        :style="panelStyle"
        @mouseenter="cancelClose"
        @mouseleave="leave"
        @focusout="onFocusOut"
        @keydown.esc.prevent="close"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="text-sm font-semibold" :class="verdictClass(activeItem)">{{ verdictLabel(activeItem) }}</p>
            <dl class="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.score') }}</dt>
              <dd class="font-mono tabular-nums">{{ scoreText(activeItem) }}</dd>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.model') }}</dt>
              <dd class="break-words">{{ modelLabel(activeItem) }}</dd>
            </dl>
          </div>
          <button type="button" class="shrink-0 rounded p-1 text-gray-500 hover:text-gray-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:text-gray-400 dark:hover:text-gray-100" :aria-label="t('channelMonitorV3.quality.close')" @click="close">
            <Icon name="x" size="sm" />
          </button>
        </div>
        <p v-if="artworkLoading" class="mt-3" role="status">{{ t('channelMonitorV3.quality.loading') }}</p>
        <div v-else-if="artworkError" class="mt-3" role="alert">
          <p>{{ t('channelMonitorV3.quality.previewFailed') }}</p>
          <button type="button" class="btn btn-ghost btn-sm mt-2" @click="retryArtwork">{{ t('channelMonitorV3.quality.retry') }}</button>
        </div>
        <div v-else-if="artworkDocument" class="quality-stage mt-3 rounded border border-gray-200 bg-white dark:border-dark-600">
          <iframe :srcdoc="artworkDocument" sandbox="" referrerpolicy="no-referrer" :title="t('channelMonitorV3.quality.htmlPreview')" class="quality-frame" />
        </div>
        <p v-else class="mt-3 text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.noPreview') }}</p>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getQualityArtwork, getQualityHistory, type MonitorLatestQuality } from '@/api/channelMonitorV2'
import Icon from '@/components/icons/Icon.vue'
import { qualityArtworkDocument } from '@/features/channel-monitor-v2/qualityArtwork'
import type { GroupQualityRun } from '@/api/admin/qualityCheck'

const props = defineProps<{
  groupId?: number
  enabled?: boolean
  latest?: MonitorLatestQuality | null
  qualityRun?: GroupQualityRun
  history?: MonitorLatestQuality[] | null
}>()
// Kept so MonitorSettingsPanel can pass qualityRun without a fallthrough attribute.
void props.qualityRun

const { t } = useI18n()
const panelId = `quality-preview-${useId()}`
const anchor = ref<HTMLElement>()
const panel = ref<HTMLElement>()
const open = ref(false)
const pinned = ref(false)
const activeId = ref('')
const panelStyle = ref<Record<string, string>>({})
const historyLoading = ref(false)
const historySettled = ref(false)
const fetchedItems = ref<MonitorLatestQuality[]>([])
const artworkLoading = ref(false)
const artworkError = ref(false)
const artworkDocument = ref('')
const artworkOwner = ref('')
let alive = true
let historyRequest: AbortController | undefined
let historyVersion = 0
let artworkRequest: AbortController | undefined
let artworkVersion = 0
let closeTimer: ReturnType<typeof setTimeout> | undefined
const PASS_SCORE = 80
const PANEL_GUTTER = 16
const PANEL_MAX_WIDTH = 640

function historyIsProvided() {
  return Array.isArray(props.history)
}
function isValidScore(score: unknown): score is number {
  return typeof score === 'number' && Number.isFinite(score) && score >= 0 && score <= 100
}
function verdictOf(item?: MonitorLatestQuality | null) {
  const score = item?.score
  if (!isValidScore(score)) return 'unscored'
  if (typeof item?.passed === 'boolean') return item.passed ? 'pass' : 'fail'
  return score >= PASS_SCORE ? 'pass' : 'fail'
}
function normalize(items: readonly MonitorLatestQuality[] | null | undefined) {
  if (!items?.length) return []
  const seen = new Set<string>()
  const unique: Array<{ item: MonitorLatestQuality; index: number }> = []
  items.forEach((item, index) => {
    if (!item || typeof item.result_id !== 'string' || !item.result_id || seen.has(item.result_id)) return
    seen.add(item.result_id)
    unique.push({ item, index })
  })
  unique.sort((a, b) => {
    const aTime = Date.parse(a.item.checked_at)
    const bTime = Date.parse(b.item.checked_at)
    const aOk = Number.isFinite(aTime)
    const bOk = Number.isFinite(bTime)
    if (aOk && bOk && aTime !== bTime) return bTime - aTime
    if (aOk !== bOk) return aOk ? -1 : 1
    return a.index - b.index
  })
  return unique.slice(0, 5).map(entry => entry.item)
}
const displayItems = computed(() => {
  if (historyIsProvided()) return normalize(props.history)
  const fetched = normalize(fetchedItems.value)
  if (fetched.length) return fetched
  if (historySettled.value && props.latest?.result_id) return normalize([props.latest])
  return []
})
const visible = computed(() => props.enabled !== false && Boolean(props.enabled || props.latest || displayItems.value.length))
const activeItem = computed(() => displayItems.value.find(item => item.result_id === activeId.value) ?? null)
const heading = computed(() => t('channelMonitorV3.quality.historyHeading', { count: displayItems.value.length }))

function verdictLabel(item: MonitorLatestQuality) {
  return t(`channelMonitorV3.quality.verdict.${verdictOf(item)}`)
}
function scoreText(item: MonitorLatestQuality) {
  return isValidScore(item.score) ? String(item.score) : '—'
}
function modelLabel(item: MonitorLatestQuality) {
  const model = item.model_id?.trim()
  return model || t('channelMonitorV3.quality.defaultModel')
}
function chipLabel(item: MonitorLatestQuality) {
  return `${formatHm(item.checked_at)} ${verdictLabel(item)}`
}
function dotClass(item: MonitorLatestQuality) {
  const verdict = verdictOf(item)
  if (verdict === 'pass') return 'bg-emerald-500'
  if (verdict === 'fail') return 'bg-red-500'
  return 'bg-gray-400'
}
function verdictClass(item: MonitorLatestQuality) {
  const verdict = verdictOf(item)
  if (verdict === 'pass') return 'text-emerald-700 dark:text-emerald-400'
  if (verdict === 'fail') return 'text-red-600 dark:text-red-400'
  return 'text-gray-500 dark:text-gray-400'
}
function formatHm(value: string) {
  const time = Date.parse(value)
  if (!Number.isFinite(time)) return '—'
  const date = new Date(time)
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}
function cancelClose() {
  clearTimeout(closeTimer)
}
function clearArtwork() {
  artworkRequest?.abort()
  artworkRequest = undefined
  artworkVersion += 1
  artworkLoading.value = false
  artworkError.value = false
  artworkDocument.value = ''
  artworkOwner.value = ''
}
function close() {
  cancelClose()
  open.value = false
  pinned.value = false
  activeId.value = ''
  clearArtwork()
}
function place(trigger: HTMLElement) {
  const rect = trigger.getBoundingClientRect()
  const width = Math.min(PANEL_MAX_WIDTH, Math.max(0, window.innerWidth - PANEL_GUTTER * 2))
  const above = Math.max(0, rect.top - 24)
  const below = Math.max(0, window.innerHeight - rect.bottom - 24)
  const placeBelow = below >= above
  const height = Math.min(720, placeBelow ? below : above)
  const top = placeBelow ? rect.bottom + 8 : Math.max(PANEL_GUTTER, rect.top - height - 8)
  const left = Math.max(PANEL_GUTTER, Math.min(rect.left, window.innerWidth - width - PANEL_GUTTER))
  panelStyle.value = { width: `${width}px`, maxHeight: `${height}px`, left: `${left}px`, top: `${top}px` }
}
async function loadArtwork(item: MonitorLatestQuality) {
  const resultId = item.result_id
  const groupId = props.groupId
  if (!alive || !open.value || activeId.value !== resultId) return
  if (!item.has_artwork || typeof groupId !== 'number') {
    clearArtwork()
    return
  }
  const owner = `${groupId}:${resultId}`
  if (artworkOwner.value === owner && (artworkDocument.value || artworkLoading.value || artworkError.value)) return
  artworkRequest?.abort()
  const current = new AbortController()
  artworkRequest = current
  const version = ++artworkVersion
  artworkOwner.value = owner
  artworkLoading.value = true
  artworkError.value = false
  artworkDocument.value = ''
  try {
    const html = await getQualityArtwork(resultId, groupId, current.signal)
    if (!alive || current.signal.aborted || artworkRequest !== current || version !== artworkVersion) return
    if (props.groupId !== groupId || activeId.value !== resultId || !open.value) return
    const document = qualityArtworkDocument(html)
    if (!alive || props.groupId !== groupId || activeId.value !== resultId || !open.value || version !== artworkVersion) return
    artworkDocument.value = document
  } catch {
    if (!alive || current.signal.aborted || artworkRequest !== current || version !== artworkVersion) return
    if (props.groupId === groupId && activeId.value === resultId) artworkError.value = true
  } finally {
    if (artworkRequest === current) {
      artworkRequest = undefined
      artworkLoading.value = false
    }
  }
}
function show(item: MonitorLatestQuality, event: Event) {
  cancelClose()
  const trigger = event.currentTarget
  if (!(trigger instanceof HTMLElement)) return
  const switched = activeId.value !== item.result_id || props.groupId + ':' + item.result_id !== artworkOwner.value
  activeId.value = item.result_id
  place(trigger)
  open.value = true
  if (switched || (!artworkDocument.value && !artworkLoading.value && !artworkError.value)) void loadArtwork(item)
}
function togglePinned(item: MonitorLatestQuality, event: Event) {
  if (pinned.value && open.value && activeId.value === item.result_id) {
    close()
    return
  }
  pinned.value = true
  show(item, event)
}
function retryArtwork() {
  const item = activeItem.value
  if (!item || !open.value) return
  pinned.value = true
  artworkError.value = false
  artworkOwner.value = ''
  void loadArtwork(item)
}
function leave() {
  cancelClose()
  if (pinned.value) return
  closeTimer = setTimeout(() => {
    const active = document.activeElement
    if (active instanceof Node && (anchor.value?.contains(active) || panel.value?.contains(active))) return
    close()
  }, 120)
}
function onFocusOut(event: FocusEvent) {
  const next = event.relatedTarget
  if (next instanceof Node && (anchor.value?.contains(next) || panel.value?.contains(next))) return
  leave()
}
function outside(event: PointerEvent) {
  if (!open.value) return
  const target = event.target
  if (target instanceof Node && (anchor.value?.contains(target) || panel.value?.contains(target))) return
  close()
}
function onDocumentKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !open.value) return
  event.preventDefault()
  close()
}
function onViewportChange() {
  if (open.value) close()
}
function shouldLoadHistory() {
  return !historyIsProvided() && props.enabled !== false && typeof props.groupId === 'number' && visible.value
}
async function syncHistory() {
  historyRequest?.abort()
  historyRequest = undefined
  const version = ++historyVersion
  if (!shouldLoadHistory()) {
    historyLoading.value = false
    if (historyIsProvided()) fetchedItems.value = []
    return
  }
  const groupId = props.groupId as number
  const current = new AbortController()
  historyRequest = current
  historySettled.value = false
  historyLoading.value = true
  fetchedItems.value = []
  try {
    const items = await getQualityHistory(groupId, current.signal)
    if (!alive || current.signal.aborted || historyRequest !== current || version !== historyVersion) return
    if (props.groupId !== groupId || historyIsProvided() || props.enabled === false) return
    fetchedItems.value = items
  } catch {
    if (!alive || current.signal.aborted || historyRequest !== current || version !== historyVersion) return
    fetchedItems.value = []
  } finally {
    if (historyRequest === current) historyRequest = undefined
    if (alive && version === historyVersion) {
      historyLoading.value = false
      historySettled.value = true
    }
  }
}
watch(() => [props.groupId, props.enabled, props.latest?.result_id, historyIsProvided()] as const, () => { void syncHistory() }, { immediate: true })
watch(() => props.groupId, (groupId, previous) => {
  if (previous != null && groupId !== previous) close()
})
watch(visible, shown => { if (!shown) close() })
watch(displayItems, items => {
  if (activeId.value && !items.some(item => item.result_id === activeId.value)) close()
})
watch(() => [open.value, activeItem.value?.result_id, activeItem.value?.has_artwork, props.groupId] as const, () => {
  if (open.value && activeItem.value) void loadArtwork(activeItem.value)
})
onMounted(() => {
  document.addEventListener('pointerdown', outside)
  document.addEventListener('keydown', onDocumentKeydown)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange)
})
onBeforeUnmount(() => {
  alive = false
  close()
  historyRequest?.abort()
  historyRequest = undefined
  historyVersion += 1
  document.removeEventListener('pointerdown', outside)
  document.removeEventListener('keydown', onDocumentKeydown)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange)
})
</script>

<style scoped>
.quality-preview-panel { overflow-wrap: anywhere; }
/* 960x640 layout viewport scaled to 600x400. Do not refit the HTML to the popup width. */
.quality-stage {
  position: relative;
  width: 600px;
  height: 400px;
  flex: none;
  overflow: hidden;
  background: #fff;
}
.quality-frame {
  position: absolute;
  top: 0;
  left: 0;
  width: 960px;
  height: 640px;
  border: 0;
  background: #fff;
  transform: scale(0.625);
  transform-origin: top left;
}
</style>
