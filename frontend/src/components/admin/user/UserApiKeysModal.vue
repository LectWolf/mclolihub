<template>
  <BaseDialog :show="show" :title="t('admin.users.userApiKeys')" width="wide" @close="handleClose">
    <div v-if="user" class="space-y-4">
      <div class="flex items-center gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <div class="flex h-10 w-10 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
          <span class="text-lg font-medium text-primary-700 dark:text-primary-300">{{ user.email.charAt(0).toUpperCase() }}</span>
        </div>
        <div><p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p><p class="text-sm text-gray-500 dark:text-dark-400">{{ user.username }}</p></div>
      </div>
      <div v-if="loading" class="flex justify-center py-8"><svg class="h-8 w-8 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg></div>
      <div v-else-if="apiKeys.length === 0" class="py-8 text-center"><p class="text-sm text-gray-500">{{ t('admin.users.noApiKeys') }}</p></div>
      <div v-else ref="scrollContainerRef" class="max-h-96 space-y-3 overflow-y-auto" @scroll="closeGroupSelector">
        <div v-for="key in apiKeys" :key="key.id" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex items-start justify-between">
            <div class="min-w-0 flex-1">
              <div class="mb-1 flex items-center gap-2"><span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span><span :class="['badge text-xs', key.status === 'active' ? 'badge-success' : 'badge-danger']">{{ key.status }}</span></div>
              <p class="truncate font-mono text-sm text-gray-500">{{ key.key.substring(0, 20) }}...{{ key.key.substring(key.key.length - 8) }}</p>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap gap-4 text-xs text-gray-500">
            <div class="flex items-center gap-1">
              <span>{{ t('admin.users.group') }}:</span>
              <button
                :ref="(el) => setGroupButtonRef(key.id, el)"
                @click="openGroupSelector(key)"
                class="-mx-1 -my-0.5 flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                :disabled="updatingKeyIds.has(key.id)"
              >
                <GroupBadge
                  v-if="key.group_id && key.group"
                  :name="key.group.name"
                  :platform="key.group.platform"
                  :subscription-type="key.group.subscription_type"
                  :rate-multiplier="key.group.rate_multiplier"
                  :peak-rate-enabled="key.group.peak_rate_enabled"
                  :peak-start="key.group.peak_start"
                  :peak-end="key.group.peak_end"
                  :peak-rate-multiplier="key.group.peak_rate_multiplier"
                />
                <span v-else class="text-gray-400 italic">{{ t('admin.users.none') }}</span>
                <svg v-if="updatingKeyIds.has(key.id)" class="h-3 w-3 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg>
                <svg v-else class="h-3 w-3 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9" /></svg>
              </button>
            </div>
            <div class="flex items-center gap-1"><span>{{ t('admin.users.columns.created') }}: {{ formatDateTime(key.created_at) }}</span></div>
          </div>
          <div class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700">
            <div class="flex flex-wrap items-end gap-2">
              <label class="min-w-32 flex-1">
                <span class="mb-1 block text-[11px] text-gray-400">路由方式</span>
                <select v-model="drafts[key.id].route_mode" class="input input-sm w-full">
                  <option value="fixed">固定分组</option>
                  <option value="cheapest">低价优先</option>
                  <option value="fastest">响应优先</option>
                  <option value="custom">自定义顺序</option>
                </select>
              </label>
              <label v-if="drafts[key.id].route_mode !== 'fixed'" class="min-w-32 flex-1">
                <span class="mb-1 block text-[11px] text-gray-400">平台范围</span>
                <select v-model="drafts[key.id].route_platform" class="input input-sm w-full">
                  <option value="auto">自动兼容（OpenAI）</option>
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic</option>
                  <option value="grok">Grok</option>
                </select>
              </label>
              <label class="w-28">
                <span class="mb-1 block text-[11px] text-gray-400">最大倍率</span>
                <input v-model.number="drafts[key.id].max_rate_multiplier" type="number" min="0" step="0.001" class="input input-sm w-full" placeholder="不限" />
              </label>
              <button type="button" class="btn btn-primary btn-sm" :disabled="savingKeyIds.has(key.id)" @click="saveRouting(key)">
                {{ savingKeyIds.has(key.id) ? '保存中…' : '保存路由' }}
              </button>
            </div>
            <div v-if="drafts[key.id].route_mode === 'cheapest' || drafts[key.id].route_mode === 'fastest'" class="mt-2 flex flex-wrap gap-x-3 gap-y-1">
              <label v-for="group in allGroups" :key="`disabled-${key.id}-${group.id}`" class="inline-flex items-center gap-1 text-xs text-gray-500">
                <input v-model="drafts[key.id].disabled_group_ids" type="checkbox" :value="group.id" class="checkbox checkbox-xs" />
                {{ group.name }}
              </label>
            </div>
            <div v-if="drafts[key.id].route_mode === 'custom'" class="mt-2 space-y-1">
              <div class="text-[11px] text-gray-400">自定义顺序</div>
              <div v-for="(groupId, index) in drafts[key.id].custom_group_ids" :key="`custom-${key.id}-${groupId}`" class="flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
                <span class="w-4 text-center tabular-nums text-gray-400">{{ index + 1 }}</span>
                <span class="min-w-0 flex-1 truncate">{{ allGroups.find((group) => group.id === groupId)?.name || `#${groupId}` }}</span>
                <button type="button" class="btn btn-ghost btn-icon h-6 w-6" :disabled="index === 0" title="上移" @click="moveCustomGroup(key.id, index, -1)">↑</button>
                <button type="button" class="btn btn-ghost btn-icon h-6 w-6" :disabled="index === drafts[key.id].custom_group_ids.length - 1" title="下移" @click="moveCustomGroup(key.id, index, 1)">↓</button>
              </div>
              <div class="flex flex-wrap gap-1">
                <button v-for="group in allGroups.filter((group) => !drafts[key.id].custom_group_ids.includes(group.id))" :key="`add-${key.id}-${group.id}`" type="button" class="btn btn-secondary btn-xs" @click="drafts[key.id].custom_group_ids.push(group.id)">+ {{ group.name }}</button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </BaseDialog>

  <!-- Group Selector Dropdown -->
  <Teleport to="body">
    <div
      v-if="groupSelectorKeyId !== null && dropdownPosition"
      ref="dropdownRef"
      class="animate-in fade-in slide-in-from-top-2 fixed z-[100000020] w-64 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 duration-200 dark:bg-dark-800 dark:ring-white/10"
      :style="{ top: dropdownPosition.top + 'px', left: dropdownPosition.left + 'px' }"
    >
      <div class="max-h-64 overflow-y-auto p-1.5">
        <!-- Unbind option -->
        <button
          @click="changeGroup(selectedKeyForGroup!, null)"
          :class="[
            'flex w-full items-center rounded-lg px-3 py-2 text-sm transition-colors',
            !selectedKeyForGroup?.group_id
              ? 'bg-primary-50 dark:bg-primary-900/20'
              : 'hover:bg-gray-100 dark:hover:bg-dark-700'
          ]"
        >
          <span class="text-gray-500 italic">{{ t('admin.users.none') }}</span>
          <svg
            v-if="!selectedKeyForGroup?.group_id"
            class="ml-auto h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400"
            fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"
          ><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>
        </button>
        <!-- Group options -->
        <button
          v-for="group in allGroups"
          :key="group.id"
          @click="changeGroup(selectedKeyForGroup!, group.id)"
          :class="[
            'flex w-full items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors',
            selectedKeyForGroup?.group_id === group.id
              ? 'bg-primary-50 dark:bg-primary-900/20'
              : 'hover:bg-gray-100 dark:hover:bg-dark-700'
          ]"
        >
          <GroupOptionItem
            :name="group.name"
            :platform="group.platform"
            :subscription-type="group.subscription_type"
            :rate-multiplier="group.rate_multiplier"
            :peak-rate-enabled="group.peak_rate_enabled"
            :peak-start="group.peak_start"
            :peak-end="group.peak_end"
            :peak-rate-multiplier="group.peak_rate_multiplier"
            :description="group.description"
            :selected="selectedKeyForGroup?.group_id === group.id"
          />
        </button>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser, AdminGroup, ApiKey } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits(['close'])
const { t } = useI18n()
const appStore = useAppStore()

const apiKeys = ref<ApiKey[]>([])
const allGroups = ref<AdminGroup[]>([])
const loading = ref(false)
const updatingKeyIds = ref(new Set<number>())
const savingKeyIds = ref(new Set<number>())
const drafts = ref<Record<number, {
  route_mode: ApiKey['route_mode']
  route_platform: ApiKey['route_platform']
  max_rate_multiplier: number | null
  disabled_group_ids: number[]
  custom_group_ids: number[]
}>>({})
const groupSelectorKeyId = ref<number | null>(null)
const dropdownPosition = ref<{ top: number; left: number } | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const scrollContainerRef = ref<HTMLElement | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())

const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    groupButtonRefs.value.set(keyId, el)
  } else {
    groupButtonRefs.value.delete(keyId)
  }
}

watch(() => props.show, (v) => {
  if (v && props.user) {
    load()
    loadGroups()
  } else {
    closeGroupSelector()
  }
})

const load = async () => {
  if (!props.user) return
  loading.value = true
  groupButtonRefs.value.clear()
  try {
    const res = await adminAPI.users.getUserApiKeys(props.user.id)
    apiKeys.value = res.items || []
    drafts.value = Object.fromEntries(apiKeys.value.map((key) => [key.id, {
      route_mode: key.route_mode || 'fixed',
      route_platform: key.route_platform || 'auto',
              max_rate_multiplier: key.max_rate_multiplier ?? null,
      disabled_group_ids: (key.group_preferences || []).filter((item) => item.disabled).map((item) => item.group_id),
      custom_group_ids: (key.group_preferences || []).filter((item) => !item.disabled).sort((a, b) => a.position - b.position).map((item) => item.group_id),
    }]))
  } catch (error) {
    console.error('Failed to load API keys:', error)
  } finally {
    loading.value = false
  }
}

const moveCustomGroup = (keyId: number, index: number, delta: number) => {
  const ids = drafts.value[keyId]?.custom_group_ids
  if (!ids) return
  const next = index + delta
  if (next < 0 || next >= ids.length) return
  const [item] = ids.splice(index, 1)
  ids.splice(next, 0, item)
}

const saveRouting = async (key: ApiKey) => {
  const draft = drafts.value[key.id]
  if (!draft) return
  savingKeyIds.value.add(key.id)
  try {
    const updated = await adminAPI.apiKeys.updateApiKeyRouting(key.id, {
      route_mode: draft.route_mode,
      route_platform: draft.route_mode === 'fixed' ? 'auto' : draft.route_platform,
      max_rate_multiplier: draft.max_rate_multiplier,
      disabled_group_ids: draft.disabled_group_ids,
      custom_group_ids: draft.custom_group_ids,
    })
    const index = apiKeys.value.findIndex((item) => item.id === key.id)
    if (index >= 0) apiKeys.value[index] = updated
    appStore.showSuccess('API 密钥路由已更新')
  } catch (error: any) {
    appStore.showError(error?.response?.data?.detail || error?.message || 'API 密钥路由更新失败')
  } finally {
    savingKeyIds.value.delete(key.id)
  }
}

const loadGroups = async () => {
  try {
    const groups = await adminAPI.groups.getAll()
    allGroups.value = groups
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}

const DROPDOWN_HEIGHT = 272 // max-h-64 = 16rem = 256px + padding
const DROPDOWN_GAP = 4

const openGroupSelector = (key: ApiKey) => {
  if (groupSelectorKeyId.value === key.id) {
    closeGroupSelector()
  } else {
    const buttonEl = groupButtonRefs.value.get(key.id)
    if (buttonEl) {
      const rect = buttonEl.getBoundingClientRect()
      const spaceBelow = window.innerHeight - rect.bottom
      const openUpward = spaceBelow < DROPDOWN_HEIGHT && rect.top > spaceBelow
      dropdownPosition.value = {
        top: openUpward ? rect.top - DROPDOWN_HEIGHT - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP,
        left: rect.left
      }
    }
    groupSelectorKeyId.value = key.id
  }
}

const closeGroupSelector = () => {
  groupSelectorKeyId.value = null
  dropdownPosition.value = null
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  closeGroupSelector()
  if (key.group_id === newGroupId || (!key.group_id && newGroupId === null)) return

  updatingKeyIds.value.add(key.id)
  try {
    const result = await adminAPI.apiKeys.updateApiKeyGroup(key.id, newGroupId)
    // Update local data
    const idx = apiKeys.value.findIndex((k) => k.id === key.id)
    if (idx !== -1) {
      apiKeys.value[idx] = result.api_key
    }
    if (result.auto_granted_group_access && result.granted_group_name) {
      appStore.showSuccess(t('admin.users.groupChangedWithGrant', { group: result.granted_group_name }))
    } else {
      appStore.showSuccess(t('admin.users.groupChangedSuccess'))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.users.groupChangeFailed'))
  } finally {
    updatingKeyIds.value.delete(key.id)
  }
}

const handleKeyDown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && groupSelectorKeyId.value !== null) {
    event.stopPropagation()
    closeGroupSelector()
  }
}

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (dropdownRef.value && !dropdownRef.value.contains(target)) {
    // Check if the click is on one of the group trigger buttons
    for (const el of groupButtonRefs.value.values()) {
      if (el.contains(target)) return
    }
    closeGroupSelector()
  }
}

const handleClose = () => {
  closeGroupSelector()
  emit('close')
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeyDown, true)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeyDown, true)
})
</script>
