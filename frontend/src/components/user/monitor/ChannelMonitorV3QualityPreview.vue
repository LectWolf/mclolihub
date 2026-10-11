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
          <p v-if="latest.reason" class="mt-2 whitespace-pre-wrap break-words">{{ latest.reason }}</p>
          <p v-if="loading" class="mt-3" role="status">{{ t('channelMonitorV3.quality.loading') }}</p>
          <div v-else-if="error" class="mt-3" role="alert">
            <p>{{ t('channelMonitorV3.quality.previewFailed') }}</p>
            <button type="button" class="btn btn-ghost btn-sm mt-2" @click="loadPreview">{{ t('channelMonitorV3.quality.retry') }}</button>
          </div>
          <img v-else-if="imageUrl" :src="imageUrl" :alt="t('channelMonitorV3.quality.imageLabel', { model: latest.model_id || t('channelMonitorV3.quality.defaultModel') })" class="mt-3 max-h-64 w-full object-contain" @error="imageFailed" />
          <p v-else class="mt-3 text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.quality.noPreview') }}</p>
        </template>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getQualityPreview, type MonitorLatestQuality } from '@/api/channelMonitorV2'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ groupId?: number; enabled?: boolean; latest?: MonitorLatestQuality | null }>()
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
let request: AbortController | undefined
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

function cancelClose() { clearTimeout(closeTimer) }
function releaseImage() {
  if (imageUrl.value) URL.revokeObjectURL(imageUrl.value)
  imageUrl.value = ''
}
function reset() {
  request?.abort()
  request = undefined
  loading.value = false
  error.value = false
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
function show() {
  cancelClose()
  const rect = trigger.value?.getBoundingClientRect()
  if (!rect) return
  const width = Math.min(360, window.innerWidth - 32)
  const above = Math.max(0, rect.top - 24)
  const below = Math.max(0, window.innerHeight - rect.bottom - 24)
  const placeBelow = below >= above
  const height = Math.min(420, placeBelow ? below : above)
  const top = placeBelow ? rect.bottom + 8 : Math.max(16, rect.top - height - 8)
  panelStyle.value = { width: `${width}px`, maxHeight: `${height}px`, left: `${Math.max(16, Math.min(rect.left, window.innerWidth - width - 16))}px`, top: `${top}px` }
  open.value = true
  if (!error.value) void loadPreview()
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
watch([() => props.groupId, () => props.enabled, () => props.latest?.result_id, () => props.latest?.has_preview], () => {
  reset()
  if (!visible.value) close()
  else if (open.value) void loadPreview()
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
