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
    <div v-else-if="historyLoading" class="mt-2 flex gap-1.5" role="status" :aria-label="t('channelMonitorV3.quality.loading')">
      <span v-for="index in 3" :key="index" class="quality-chip-skeleton" />
    </div>
    <p v-else class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.status.pending') }}</p>

    <Teleport to="body">
      <div
        v-if="open && activeItem"
        :id="panelId"
        ref="panel"
        role="dialog"
        :aria-label="activeItem.has_artwork ? t('channelMonitorV3.quality.htmlOutput') : t('channelMonitorV3.quality.noPreview')"
        class="quality-preview-panel fixed z-[60] overflow-y-auto overscroll-contain rounded-lg border border-gray-200 bg-white p-3 text-xs text-gray-800 shadow-lg dark:border-dark-600 dark:bg-dark-800 dark:text-gray-100"
        :style="panelStyle"
        @mouseenter="cancelClose"
        @mouseleave="leave"
        @focusout="onFocusOut"
        @keydown.esc.prevent="close"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="text-sm font-semibold" :class="activeItem.has_artwork ? 'text-blue-700 dark:text-blue-300' : 'text-gray-700 dark:text-gray-200'">{{ activeItem.has_artwork ? t('channelMonitorV3.quality.htmlOutput') : t('channelMonitorV3.quality.noPreview') }}</p>
            <dl class="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
              <dt class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.diagnosticFields.group_name') }}</dt>
              <dd class="break-words">{{ groupLabel }}<span v-if="typeof groupId === 'number'" class="ml-1 text-gray-500 dark:text-gray-400">(#{{ groupId }})</span></dd>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.model') }}</dt>
              <dd class="break-words">{{ modelLabel(activeItem) }}</dd>
              <dt class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.checkedAt') }}</dt>
              <dd class="font-mono tabular-nums">{{ formatHm(activeItem.checked_at) }}</dd>
            </dl>
          </div>
          <button type="button" class="shrink-0 rounded p-1 text-gray-500 hover:text-gray-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 dark:text-gray-400 dark:hover:text-gray-100" :aria-label="t('channelMonitorV3.quality.close')" @click="close">
            <Icon name="x" size="sm" />
          </button>
        </div>

        <div v-if="artworkLoading" class="quality-stage mt-3 rounded border border-gray-200 bg-white dark:border-dark-600" role="status" :aria-label="t('channelMonitorV3.quality.loading')">
          <div class="quality-preview-skeleton" aria-hidden="true">
            <span class="quality-preview-skeleton-line quality-preview-skeleton-line-wide" />
            <span class="quality-preview-skeleton-line" />
            <span class="quality-preview-skeleton-line quality-preview-skeleton-line-short" />
          </div>
        </div>
        <div v-else-if="artworkError" class="quality-stage mt-3 rounded border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-900" role="alert">
          <p>{{ t('channelMonitorV3.quality.previewFailed') }}</p>
          <button type="button" class="btn btn-ghost btn-sm mt-2" @click="retryArtwork">{{ t('channelMonitorV3.quality.retry') }}</button>
        </div>
        <div v-else-if="artworkDocument" class="quality-stage mt-3 rounded border border-gray-200 bg-white dark:border-dark-600">
          <iframe :srcdoc="artworkDocument" sandbox="" scrolling="no" referrerpolicy="no-referrer" :title="t('channelMonitorV3.quality.htmlPreview')" class="quality-frame" />
        </div>
        <div v-else class="mt-3 space-y-3">
          <p v-if="activeItem.reason" class="whitespace-pre-wrap break-words text-gray-600 dark:text-gray-300"><span class="font-medium">{{ t('channelMonitorV3.quality.failureReason') }}: </span>{{ activeItem.reason }}</p>
          <p v-if="rawOutputLoading" role="status" class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.loading') }}</p>
          <div v-else-if="rawOutputError" role="alert">
            <p>{{ t('channelMonitorV3.quality.rawOutputFailed') }}</p>
            <button type="button" class="btn btn-ghost btn-sm mt-2" @click="retryRawOutput">{{ t('channelMonitorV3.quality.retry') }}</button>
          </div>
          <template v-else-if="rawOutput">
            <div class="flex items-center justify-between gap-3">
              <p class="font-medium">{{ t('channelMonitorV3.quality.rawOutput') }}</p>
              <button type="button" class="btn btn-ghost btn-sm" @click="copyRawOutput">{{ t('channelMonitorV3.quality.copyRawOutput') }}</button>
            </div>
            <pre class="quality-raw-output overflow-auto rounded border border-gray-200 bg-gray-50 p-2 font-mono text-[11px] dark:border-dark-600 dark:bg-dark-900" tabindex="0" :aria-label="t('channelMonitorV3.quality.rawOutput')">{{ rawOutput }}</pre>
            <p class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.rawOutputHint') }}</p>
          </template>
          <p v-else class="text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.noRawOutput') }}</p>
        </div>

        <section v-if="adminDiagnostics && typeof groupId === 'number'" class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700" :aria-label="t('channelMonitorV3.quality.diagnosticsTitle')">
          <button v-if="!diagnosticsResult" type="button" class="btn btn-ghost btn-sm" :disabled="diagnosticsLoading" @click="loadDiagnostics">
            {{ diagnosticsLoading ? t('channelMonitorV3.quality.diagnosticsLoading') : diagnosticsError ? t('channelMonitorV3.quality.retry') : t('channelMonitorV3.quality.viewDiagnostics') }}
          </button>
          <p v-if="diagnosticsLoading" role="status">{{ t('channelMonitorV3.quality.diagnosticsLoading') }}</p>
          <p v-else-if="diagnosticsError" role="alert">{{ t('channelMonitorV3.quality.diagnosticsFailed') }}</p>
          <template v-else-if="diagnosticsResult">
            <div class="flex items-center justify-between gap-3">
              <p class="font-medium">{{ t('channelMonitorV3.quality.diagnosticsTitle') }}</p>
              <button type="button" class="btn btn-ghost btn-sm" @click="copyDiagnostics">{{ t('channelMonitorV3.quality.copyDiagnostics') }}</button>
            </div>
            <pre class="quality-raw-output mt-2 rounded border border-gray-200 bg-gray-50 p-2 font-mono text-[11px] dark:border-dark-600 dark:bg-dark-900" tabindex="0">{{ diagnosticsText }}</pre>
            <p v-if="!diagnosticsResult.attempts?.length" class="mt-2 text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.diagnosticsLegacy') }}</p>
            <p v-if="diagnosticsResult.details_truncated" class="mt-2 text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.diagnosticsTruncated') }}</p>
          </template>
        </section>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getQualityArtwork, getQualityHistory, getQualityRawOutput, type MonitorLatestQuality } from '@/api/channelMonitorV2'
import { getQualityDiagnostics, type GroupQualityDiagnosticResult } from '@/api/admin/qualityCheck'
import Icon from '@/components/icons/Icon.vue'
import { qualityArtworkDocument } from '@/features/channel-monitor-v2/qualityArtwork'
import { useClipboard } from '@/composables/useClipboard'

const props = withDefaults(defineProps<{
  groupId?: number
  groupName?: string
  enabled?: boolean
  latest?: MonitorLatestQuality | null
  history?: MonitorLatestQuality[] | null
  adminDiagnostics?: boolean
}>(), { adminDiagnostics: false })

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
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
const rawOutput = ref('')
const rawOutputLoading = ref(false)
const rawOutputError = ref(false)
const diagnosticsResult = ref<GroupQualityDiagnosticResult | null>(null)
const diagnosticsLoading = ref(false)
const diagnosticsError = ref(false)
let diagnosticsRequest: AbortController | undefined
let diagnosticsVersion = 0
let rawOutputRequest: AbortController | undefined
let rawOutputVersion = 0
let panelObserver: ResizeObserver | undefined
let placement: { x: number; y: number } | undefined
let alive = true
let historyRequest: AbortController | undefined
let historyVersion = 0
let artworkVersion = 0
let closeTimer: ReturnType<typeof setTimeout> | undefined

const historyCache = new Map<number, MonitorLatestQuality[]>()
const artworkCache = new Map<string, string>()
const artworkPending = new Map<string, Promise<string>>()
const rawOutputCache = new Map<string, string>()
const PANEL_GUTTER = 16
const PANEL_MAX_WIDTH = 520
const PREVIEW_HEIGHT = 320

function historyIsProvided() {
  return Array.isArray(props.history)
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
const groupLabel = computed(() => props.groupName?.trim() || (props.adminDiagnostics ? diagnosticsResult.value?.group_name?.trim() : '') || t('channelMonitorV3.unknownGroup'))

const diagnosticsText = computed(() => {
  const result = diagnosticsResult.value
  if (!props.adminDiagnostics || !result) return ''
  const label = (key: string, value: string | number | boolean | undefined) => {
    const text = typeof value === 'boolean' ? t(`channelMonitorV3.quality.${value ? 'yes' : 'no'}`) : value ?? '—'
    return `${t(`channelMonitorV3.quality.diagnosticFields.${key}`)}: ${text}`
  }
  const lines = [label('group_name', groupLabel.value), label('group_id', result.group_id ?? props.groupId), label('reason', result.reason), label('attempt_count', result.diagnostics?.attempt_count ?? (result.attempts?.length || undefined))]
  if (result.diagnostics) {
    const fields = ['stage', 'configured_protocol', 'total_duration_ms', 'generation_duration_ms', 'generation_timeout_seconds', 'received_text_bytes', 'received_events', 'last_event_type', 'completion_received', 'capture_overflow', 'failure_code', 'deadline_source', 'artwork_available', 'render_duration_ms', 'render_failure_code'] as const
    for (const field of fields) {
      if (result.diagnostics[field] !== undefined) lines.push(label(field, result.diagnostics[field]))
    }
  }
  for (const attempt of result.attempts ?? []) {
    lines.push('', t('channelMonitorV3.quality.diagnosticAttempt', { number: attempt.number }))
    for (const field of ['http_status', 'error_type', 'message', 'failure_code', 'duration_ms'] as const) {
      lines.push(label(field, attempt[field]))
    }
  }
  if (result.route) {
    lines.push('', t('channelMonitorV3.quality.diagnosticRoute'), label('account_name', result.route.account_name?.trim() || undefined))
    for (const field of ['account_id', 'platform', 'endpoint', 'model', 'proxy_enabled'] as const) {
      lines.push(label(field, result.route[field]))
    }
  }
  return lines.join('\n')
})

function clearDiagnostics() {
  diagnosticsVersion += 1
  diagnosticsRequest?.abort()
  diagnosticsRequest = undefined
  diagnosticsResult.value = null
  diagnosticsLoading.value = false
  diagnosticsError.value = false
}
async function loadDiagnostics() {
  const groupId = props.groupId
  const item = activeItem.value
  if (!alive || !props.adminDiagnostics || !open.value || !item || typeof groupId !== 'number' || diagnosticsLoading.value) return
  pinned.value = true
  cancelClose()
  clearDiagnostics()
  const version = diagnosticsVersion
  const request = new AbortController()
  diagnosticsRequest = request
  diagnosticsLoading.value = true
  try {
    const result = await getQualityDiagnostics(groupId, item.result_id, request.signal)
    if (!alive || request.signal.aborted || version !== diagnosticsVersion) return
    diagnosticsResult.value = result
  } catch {
    if (!alive || request.signal.aborted || version !== diagnosticsVersion) return
    diagnosticsError.value = true
  } finally {
    if (diagnosticsRequest === request) diagnosticsRequest = undefined
    if (version === diagnosticsVersion) diagnosticsLoading.value = false
  }
}

function modelLabel(item: MonitorLatestQuality) {
  const model = item.model_id?.trim()
  return model || t('channelMonitorV3.quality.defaultModel')
}
function chipLabel(item: MonitorLatestQuality) {
  return `${formatHm(item.checked_at)} · ${item.has_artwork ? t('channelMonitorV3.quality.htmlOutput') : t('channelMonitorV3.quality.noPreview')}`
}
function dotClass(item: MonitorLatestQuality) {
  return item.has_artwork ? 'bg-blue-500' : 'bg-gray-400'
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
  clearRawOutput()
  clearDiagnostics()
  placement = undefined
}
function reposition() {
  if (!placement) return
  const width = Math.min(PANEL_MAX_WIDTH, Math.max(0, window.innerWidth - PANEL_GUTTER * 2))
  const maxHeight = Math.max(0, window.innerHeight - PANEL_GUTTER * 2)
  const fallbackHeight = activeItem.value?.has_artwork ? PREVIEW_HEIGHT + 112 : 160
  const height = Math.min(maxHeight, panel.value?.getBoundingClientRect().height || fallbackHeight)
  let left = placement.x + 12
  if (left + width > window.innerWidth - PANEL_GUTTER) left = placement.x - width - 12
  left = Math.max(PANEL_GUTTER, Math.min(left, window.innerWidth - width - PANEL_GUTTER))
  let top = placement.y + 12
  if (top + height > window.innerHeight - PANEL_GUTTER) top = placement.y - height - 12
  top = Math.max(PANEL_GUTTER, Math.min(top, window.innerHeight - height - PANEL_GUTTER))
  panelStyle.value = { width: `${width}px`, maxHeight: `${maxHeight}px`, left: `${left}px`, top: `${top}px` }
}
function place(trigger: HTMLElement, event?: Event) {
  const rect = trigger.getBoundingClientRect()
  placement = event instanceof MouseEvent && event.clientX > 0 && event.clientY > 0
    ? { x: event.clientX, y: event.clientY }
    : { x: rect.left + rect.width / 2, y: rect.bottom }
  reposition()
}
function clearRawOutput() {
  rawOutputVersion += 1
  rawOutputRequest?.abort()
  rawOutputRequest = undefined
  rawOutput.value = ''
  rawOutputLoading.value = false
  rawOutputError.value = false
}
async function loadRawOutput(item: MonitorLatestQuality) {
  clearRawOutput()
  const groupId = props.groupId
  if (!alive || !open.value || item.has_artwork || !item.has_raw_output || typeof groupId !== 'number') return
  const owner = `${groupId}:${item.result_id}`
  if (rawOutputCache.has(owner)) {
    rawOutput.value = rawOutputCache.get(owner) ?? ''
    return
  }
  const version = rawOutputVersion
  const request = new AbortController()
  rawOutputRequest = request
  rawOutputLoading.value = true
  try {
    const output = await getQualityRawOutput(item.result_id, groupId, request.signal)
    if (!alive || request.signal.aborted || version !== rawOutputVersion) return
    rawOutputCache.set(owner, output)
    rawOutput.value = output
  } catch {
    if (!alive || request.signal.aborted || version !== rawOutputVersion) return
    rawOutputError.value = true
  } finally {
    if (rawOutputRequest === request) rawOutputRequest = undefined
    if (version === rawOutputVersion) rawOutputLoading.value = false
  }
}
function retryRawOutput() {
  if (!activeItem.value || !open.value) return
  pinned.value = true
  void loadRawOutput(activeItem.value)
}
async function copyRawOutput() {
  pinned.value = true
  cancelClose()
  const focused = document.activeElement
  await copyToClipboard(rawOutput.value)
  if (focused instanceof HTMLElement && panel.value?.contains(focused)) focused.focus({ preventScroll: true })
}
async function copyDiagnostics() {
  if (!props.adminDiagnostics || !diagnosticsText.value) return
  pinned.value = true
  cancelClose()
  const focused = document.activeElement
  await copyToClipboard(diagnosticsText.value)
  if (focused instanceof HTMLElement && panel.value?.contains(focused)) focused.focus({ preventScroll: true })
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
  const cached = artworkCache.get(owner)
  if (cached) {
    artworkOwner.value = owner
    artworkDocument.value = cached
    artworkLoading.value = false
    artworkError.value = false
    return
  }
  const version = ++artworkVersion
  artworkOwner.value = owner
  artworkLoading.value = true
  artworkError.value = false
  artworkDocument.value = ''
  let pending = artworkPending.get(owner)
  if (!pending) {
    pending = getQualityArtwork(resultId, groupId).then(html => {
      const document = qualityArtworkDocument(html)
      artworkCache.set(owner, document)
      return document
    })
    artworkPending.set(owner, pending)
  }
  try {
    const document = await pending
    if (!alive || version !== artworkVersion) return
    if (props.groupId === groupId && activeId.value === resultId && open.value) artworkDocument.value = document
  } catch {
    if (!alive || version !== artworkVersion) return
    if (props.groupId === groupId && activeId.value === resultId) artworkError.value = true
  } finally {
    if (artworkPending.get(owner) === pending) artworkPending.delete(owner)
    if (version === artworkVersion) artworkLoading.value = false
  }
}
function show(item: MonitorLatestQuality, event: Event) {
  cancelClose()
  const trigger = event.currentTarget
  if (!(trigger instanceof HTMLElement)) return
  const owner = typeof props.groupId === 'number' ? `${props.groupId}:${item.result_id}` : ''
  if (artworkOwner.value !== owner) {
    artworkDocument.value = owner ? artworkCache.get(owner) ?? '' : ''
    artworkError.value = false
    artworkLoading.value = false
  }
  activeId.value = item.result_id
  place(trigger, event)
  open.value = true
  if (!artworkDocument.value && !artworkLoading.value && !artworkError.value) void loadArtwork(item)
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
  const cached = historyCache.get(groupId)
  if (cached) {
    fetchedItems.value = cached
    historyLoading.value = false
    historySettled.value = true
    return
  }
  const current = new AbortController()
  historyRequest = current
  historySettled.value = false
  historyLoading.value = true
  fetchedItems.value = []
  try {
    const items = await getQualityHistory(groupId, current.signal)
    if (!alive || current.signal.aborted || historyRequest !== current || version !== historyVersion) return
    if (props.groupId !== groupId || historyIsProvided() || props.enabled === false) return
    const normalized = normalize(items)
    historyCache.set(groupId, normalized)
    fetchedItems.value = normalized
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
watch(() => [props.groupId, props.adminDiagnostics, open.value, activeId.value] as const, clearDiagnostics, { flush: 'sync' })
watch(() => [open.value, activeItem.value?.result_id, activeItem.value?.has_artwork, activeItem.value?.has_raw_output, props.groupId] as const, () => {
  if (open.value && activeItem.value) {
    void loadArtwork(activeItem.value)
    void loadRawOutput(activeItem.value)
  }
})
watch(panel, element => {
  panelObserver?.disconnect()
  if (element && typeof ResizeObserver !== 'undefined') {
    panelObserver = new ResizeObserver(reposition)
    panelObserver.observe(element)
  }
  reposition()
}, { flush: 'post' })
watch([activeItem, artworkLoading, artworkError, artworkDocument, rawOutput, rawOutputLoading, rawOutputError, diagnosticsResult, diagnosticsLoading, diagnosticsError], reposition, { flush: 'post' })
onMounted(() => {
  document.addEventListener('pointerdown', outside)
  document.addEventListener('keydown', onDocumentKeydown)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange)
})
onBeforeUnmount(() => {
  alive = false
  panelObserver?.disconnect()
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
.quality-raw-output { max-height: min(240px, 40vh); white-space: pre-wrap; overflow-wrap: anywhere; }
.quality-stage {
  position: relative;
  width: min(480px, calc(100vw - 64px));
  height: min(320px, calc(100vh - 180px));
  min-height: 180px;
  flex: none;
  overflow: hidden;
  background: #fff;
}
.quality-frame {
  position: absolute;
  inset: 0;
  width: 960px;
  height: 640px;
  border: 0;
  background: #fff;
  transform: scale(0.5);
  transform-origin: top left;
}
.quality-chip-skeleton,
.quality-preview-skeleton-line {
  display: block;
  background: linear-gradient(90deg, rgba(148, 163, 184, .16), rgba(148, 163, 184, .32), rgba(148, 163, 184, .16));
  background-size: 200% 100%;
  animation: quality-skeleton 1.2s ease-in-out infinite;
}
.quality-chip-skeleton { width: 52px; height: 22px; border-radius: 999px; }
.quality-preview-skeleton { display: grid; gap: 12px; padding: 56px 48px; }
.quality-preview-skeleton-line { width: 72%; height: 12px; border-radius: 999px; }
.quality-preview-skeleton-line-wide { width: 92%; height: 18px; }
.quality-preview-skeleton-line-short { width: 48%; }
@keyframes quality-skeleton { from { background-position: 100% 0; } to { background-position: -100% 0; } }
@media (prefers-reduced-motion: reduce) {
  .quality-chip-skeleton,
  .quality-preview-skeleton-line { animation: none; }
}
</style>
