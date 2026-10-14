<template>
  <div v-if="visible" ref="anchor" class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700" @mouseenter="show" @mouseleave="leave" @focusout="onFocusOut">
    <button
      ref="trigger" type="button" class="flex max-w-full items-center gap-2 text-left text-xs font-medium"
      :class="statusClass" :aria-expanded="open" :aria-controls="panelId"
      @focus="show" @click="togglePinned" @keydown.esc.prevent="close"
    >
      <Icon name="eye" size="sm" />
      <span class="break-words">{{ t('channelMonitorV3.quality.title') }} · {{ statusLabel }}</span>
    </button>
    <Teleport to="body">
      <div
        v-if="open" :id="panelId" ref="panel" role="dialog" :aria-label="t('channelMonitorV3.quality.latest')"
        class="quality-preview-panel fixed z-[60] overflow-y-auto rounded-lg border border-gray-200 bg-white p-3 text-xs text-gray-800 shadow-lg dark:border-dark-600 dark:bg-dark-800 dark:text-gray-100"
        :style="panelStyle" @mouseenter="cancelClose" @mouseleave="leave" @focusout="onFocusOut" @keydown.esc.prevent="close"
      >
        <div class="flex items-center justify-between gap-2">
          <strong :class="statusClass">{{ statusLabel }}</strong>
          <button type="button" class="shrink-0 p-1" :aria-label="t('channelMonitorV3.quality.close')" @click="close"><Icon name="x" size="sm" /></button>
        </div>
        <template v-if="latest">
          <dl class="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
            <dt>{{ t('channelMonitorV3.quality.model') }}</dt><dd class="break-words">{{ latest.model_id || t('channelMonitorV3.quality.defaultModel') }}</dd>
            <dt>{{ t('channelMonitorV3.quality.checkedAt') }}</dt><dd><time :datetime="latest.checked_at">{{ checkedAt }}</time></dd>
          </dl>
          <dl v-if="latest.diagnostics" class="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
            <dt>{{ t('channelMonitorV3.quality.stage') }}</dt><dd>{{ t(`channelMonitorV3.quality.stages.${latest.diagnostics.stage}`) }}</dd>
            <dt>{{ t('channelMonitorV3.quality.generationDuration') }}</dt><dd>{{ (latest.diagnostics.generation_duration_ms / 1000).toFixed(1) }}s / {{ latest.diagnostics.generation_timeout_seconds }}s</dd>
            <dt>{{ t('channelMonitorV3.quality.receivedText') }}</dt><dd>{{ latest.diagnostics.received_text_bytes.toLocaleString() }} B</dd>
            <dt>{{ t('channelMonitorV3.quality.completionReceived') }}</dt><dd>{{ t(latest.diagnostics.completion_received ? 'channelMonitorV3.quality.yes' : 'channelMonitorV3.quality.no') }}</dd>
            <template v-if="latest.diagnostics.render_duration_ms != null">
              <dt>{{ t('channelMonitorV3.quality.renderDuration') }}</dt><dd>{{ (latest.diagnostics.render_duration_ms / 1000).toFixed(1) }}s</dd>
            </template>
            <template v-if="latest.diagnostics.render_failure_code">
              <dt>{{ t('channelMonitorV3.quality.renderFailure') }}</dt><dd>{{ t(`channelMonitorV3.quality.renderFailures.${latest.diagnostics.render_failure_code}`) }}</dd>
            </template>
          </dl>
          <p v-if="latest.reason" class="mt-2 whitespace-pre-wrap break-words">{{ latest.reason }}</p>
          <p v-if="renderingUnavailable" class="mt-2 text-amber-700 dark:text-amber-400">{{ t('channelMonitorV3.quality.renderingUnavailable') }}</p>
          <button type="button" class="btn btn-secondary btn-sm mt-3" data-quality-copy-diagnostics :disabled="copying" @click="copyDiagnostics">
            <Icon name="copy" size="xs" />
            {{ t(copied ? 'channelMonitorV3.quality.diagnosticsCopied' : 'channelMonitorV3.quality.copyDiagnostics') }}
          </button>
          <p v-if="!latest.diagnostics" class="mt-2 text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.legacyDiagnostics') }}</p>
          <div v-if="copyError" class="mt-2" role="alert">
            <p>{{ t('channelMonitorV3.quality.copyFailed') }}</p>
            <textarea :value="diagnosticText" readonly rows="10" :aria-label="t('channelMonitorV3.quality.copyDiagnostics')" class="mt-2 w-full rounded border border-gray-200 bg-gray-50 p-2 font-mono text-[11px] dark:border-dark-600 dark:bg-dark-900" @focus="selectDiagnostic" />
          </div>
          <div v-if="latest.has_artwork" class="mt-3 flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :aria-pressed="previewMode === 'html'" @click="setPreviewMode('html')">{{ t('channelMonitorV3.quality.htmlPreview') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :aria-pressed="previewMode === 'source'" @click="setPreviewMode('source')">{{ t('channelMonitorV3.quality.htmlSource') }}</button>
            <button v-if="latest.has_preview" type="button" class="btn btn-secondary btn-sm" :aria-pressed="previewMode === 'png'" @click="setPreviewMode('png')">PNG</button>
          </div>
          <template v-if="latest.has_artwork && previewMode !== 'png'">
            <p v-if="artworkLoading" class="mt-3" role="status">{{ t('channelMonitorV3.quality.loading') }}</p>
            <div v-else-if="artworkError" class="mt-3" role="alert">
              <p>{{ t('channelMonitorV3.quality.previewFailed') }}</p>
              <button type="button" class="btn btn-ghost btn-sm mt-2" @click="loadArtwork">{{ t('channelMonitorV3.quality.retry') }}</button>
            </div>
            <template v-else-if="artworkHTML">
              <textarea v-if="previewMode === 'source'" :value="artworkHTML" readonly rows="12" :aria-label="t('channelMonitorV3.quality.htmlSource')" class="mt-3 w-full rounded border border-gray-200 bg-gray-50 p-2 font-mono text-[11px] dark:border-dark-600 dark:bg-dark-900" />
              <div v-else class="relative mt-3 aspect-[3/2] w-full overflow-hidden rounded border border-gray-200 bg-white dark:border-dark-600">
                <iframe :srcdoc="artworkDocument" sandbox="" referrerpolicy="no-referrer" :title="t('channelMonitorV3.quality.htmlPreview')" class="absolute left-0 top-0 origin-top-left border-0" :style="{ width: '960px', height: '640px', transform: `scale(${previewScale})` }" />
              </div>
              <p class="mt-2 text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.htmlPreviewNote') }}</p>
            </template>
          </template>
          <template v-else>
            <p v-if="loading" class="mt-3" role="status">{{ t('channelMonitorV3.quality.loading') }}</p>
            <div v-else-if="error" class="mt-3" role="alert">
              <p>{{ t('channelMonitorV3.quality.previewFailed') }}</p>
              <button type="button" class="btn btn-ghost btn-sm mt-2" @click="loadPreview">{{ t('channelMonitorV3.quality.retry') }}</button>
            </div>
            <img v-else-if="imageUrl" :src="imageUrl" :alt="t('channelMonitorV3.quality.imageLabel', { model: latest.model_id || t('channelMonitorV3.quality.defaultModel') })" class="mt-3 max-h-64 w-full object-contain" @error="imageFailed" />
            <p v-else class="mt-3 text-gray-500 dark:text-gray-400">{{ t(latest.diagnostics?.completion_received ? 'channelMonitorV3.quality.htmlNotSaved' : 'channelMonitorV3.quality.noPreview') }}</p>
          </template>
        </template>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getQualityArtwork, getQualityPreview, type MonitorLatestQuality } from '@/api/channelMonitorV2'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { formatQualityDiagnostics } from '@/features/channel-monitor-v2/qualityDiagnostics'
import { qualityArtworkDocument } from '@/features/channel-monitor-v2/qualityArtwork'
import type { GroupQualityRun } from '@/api/admin/qualityCheck'

const props = defineProps<{ groupId?: number; enabled?: boolean; latest?: MonitorLatestQuality | null; qualityRun?: GroupQualityRun }>()
const { t, locale } = useI18n()
const panelId = `quality-preview-${useId()}`
const anchor = ref<HTMLElement>()
const trigger = ref<HTMLButtonElement>()
const panel = ref<HTMLElement>()
const open = ref(false)
const pinned = ref(false)
const loading = ref(false)
const error = ref(false)
const imageUrl = ref('')
const panelStyle = ref<Record<string, string>>({})
const artworkLoading = ref(false)
const artworkError = ref(false)
const artworkHTML = ref('')
const artworkDocument = ref('')
const previewMode = ref<'html' | 'source' | 'png'>('html')
const previewScale = ref(1)
const renderingUnavailable = computed(() => props.latest?.status === 'unknown' && Boolean(
  props.latest.diagnostics?.stage === 'rendering' || props.latest.diagnostics?.stage === 'reference_rendering' || /rendering unavailable/.test(props.latest.reason),
))
const copying = ref(false)
const copied = ref(false)
const copyError = ref(false)
const diagnosticText = computed(() => props.latest ? formatQualityDiagnostics(props.latest, props.groupId, props.qualityRun) : '')
let request: AbortController | undefined
let artworkRequest: AbortController | undefined
let closeTimer: ReturnType<typeof setTimeout> | undefined
const visible = computed(() => props.enabled !== false && Boolean(props.enabled || props.latest))
const status = computed(() => props.latest?.status ?? 'pending')
const statusLabel = computed(() => t(`channelMonitorV3.quality.status.${status.value}`))
const statusClass = computed(() => ({
  success: 'text-emerald-700 dark:text-emerald-400',
  degraded: 'text-red-600 dark:text-red-400',
  failed: 'text-amber-700 dark:text-amber-400',
  unknown: 'text-gray-500 dark:text-gray-400',
  skipped: 'text-gray-500 dark:text-gray-400',
  pending: 'text-gray-500 dark:text-gray-400',
})[status.value])
const checkedAt = computed(() => {
  const value = Date.parse(props.latest?.checked_at ?? '')
  return Number.isFinite(value) ? new Date(value).toLocaleString(locale.value) : '-'
})

async function copyDiagnostics() {
  if (!props.latest || copying.value) return
  const resultId = props.latest.result_id
  const text = diagnosticText.value
  pinned.value = true
  copying.value = true
  copyError.value = false
  try {
    const { copyToClipboard } = useClipboard()
    const success = await copyToClipboard(text, t('channelMonitorV3.quality.diagnosticsCopied'))
    if (props.latest?.result_id === resultId && open.value) { copied.value = success; copyError.value = !success }
  } catch {
    if (props.latest?.result_id === resultId && open.value) copyError.value = true
  } finally { copying.value = false }
}
function selectDiagnostic(event: FocusEvent) {
  if (event.target instanceof HTMLTextAreaElement) event.target.select()
}

function cancelClose() { clearTimeout(closeTimer) }
function releaseImage() {
  if (imageUrl.value) URL.revokeObjectURL(imageUrl.value)
  imageUrl.value = ''
}
function reset() {
  request?.abort()
  request = undefined
  artworkRequest?.abort()
  artworkRequest = undefined
  artworkLoading.value = false
  artworkError.value = false
  artworkHTML.value = ''
  artworkDocument.value = ''
  previewMode.value = 'html'
  loading.value = false
  error.value = false
  copied.value = false
  copyError.value = false
  releaseImage()
}
async function loadPreview() {
  const latest = props.latest
  if (!open.value || !latest?.has_preview || !props.groupId || loading.value || imageUrl.value) return
  request?.abort()
  const current = new AbortController()
  request = current
  loading.value = true
  error.value = false
  try {
    const blob = await getQualityPreview(latest.result_id, props.groupId, current.signal)
    // A canceled request can still resolve; never attach its image to a newer result.
    if (current.signal.aborted || request !== current) return
    imageUrl.value = URL.createObjectURL(blob)
  } catch {
    if (!current.signal.aborted && request === current) error.value = true
  } finally {
    if (request === current) {
      request = undefined
      loading.value = false
    }
  }
}
async function loadArtwork() {
  const latest = props.latest
  if (!open.value || !latest?.has_artwork || !props.groupId || artworkLoading.value || artworkHTML.value) return
  artworkRequest?.abort()
  const current = new AbortController()
  artworkRequest = current
  artworkLoading.value = true
  artworkError.value = false
  try {
    const html = await getQualityArtwork(latest.result_id, props.groupId, current.signal)
    if (current.signal.aborted || artworkRequest !== current || props.latest?.result_id !== latest.result_id) return
    const document = qualityArtworkDocument(html)
    artworkHTML.value = html
    artworkDocument.value = document
  } catch {
    if (!current.signal.aborted && artworkRequest === current) artworkError.value = true
  } finally {
    if (artworkRequest === current) { artworkRequest = undefined; artworkLoading.value = false }
  }
}
function setPreviewMode(mode: 'html' | 'source' | 'png') {
  pinned.value = true
  previewMode.value = mode
  if (mode === 'png') void loadPreview()
  else void loadArtwork()
}
function loadVisiblePreview() {
  if (props.latest?.has_artwork && previewMode.value !== 'png') {
    if (!artworkError.value) void loadArtwork()
  } else if (!error.value) void loadPreview()
}
function show() {
  cancelClose()
  const rect = trigger.value?.getBoundingClientRect()
  if (!rect) return
  const width = Math.min(props.latest?.has_artwork ? 640 : 360, window.innerWidth - 32)
  previewScale.value = Math.max(0.1, (width - 26) / 960)
  const above = Math.max(0, rect.top - 24)
  const below = Math.max(0, window.innerHeight - rect.bottom - 24)
  const placeBelow = below >= above
  const height = Math.min(props.latest?.has_artwork ? 720 : 420, placeBelow ? below : above)
  const top = placeBelow ? rect.bottom + 8 : Math.max(16, rect.top - height - 8)
  panelStyle.value = { width: `${width}px`, maxHeight: `${height}px`, left: `${Math.max(16, Math.min(rect.left, window.innerWidth - width - 16))}px`, top: `${top}px` }
  open.value = true
  loadVisiblePreview()
}
function close() {
  cancelClose()
  open.value = false
  pinned.value = false
  reset()
}
function leave() {
  cancelClose()
  if (!pinned.value) closeTimer = setTimeout(() => {
    if (!anchor.value?.contains(document.activeElement) && !panel.value?.contains(document.activeElement)) close()
  }, 120)
}
function onFocusOut(event: FocusEvent) {
  const next = event.relatedTarget
  if (next instanceof Node && (anchor.value?.contains(next) || panel.value?.contains(next))) return
  leave()
}
function togglePinned() {
  if (pinned.value) close()
  else { pinned.value = true; show() }
}
function outside(event: PointerEvent) {
  const target = event.target
  if (target instanceof Node && !anchor.value?.contains(target) && !panel.value?.contains(target)) close()
}
function imageFailed() { releaseImage(); error.value = true }
watch([() => props.groupId, () => props.enabled, () => props.latest?.result_id, () => props.latest?.has_preview, () => props.latest?.has_artwork], () => {
  reset()
  if (!visible.value) close()
  else if (open.value) { show() }
}, { flush: 'sync' })
onMounted(() => {
  document.addEventListener('pointerdown', outside)
  window.addEventListener('scroll', close)
  window.addEventListener('resize', close)
})
onBeforeUnmount(() => {
  close()
  document.removeEventListener('pointerdown', outside)
  window.removeEventListener('scroll', close)
  window.removeEventListener('resize', close)
})
</script>

<style scoped>
.quality-preview-panel { overflow-wrap: anywhere; }
</style>
