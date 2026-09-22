<template>
  <div class="space-y-3">
    <div>
      <label class="input-label">{{ t('admin.accounts.qoderProxy.edition') }}</label>
      <div class="mt-1 flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
        <button
          type="button"
          class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition-all"
          :class="region === 'cn' ? 'bg-white text-emerald-800 shadow-sm dark:bg-dark-600 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-400'"
          @click="emit('update:region', 'cn')"
        >
          {{ t('admin.accounts.qoderProxy.regionCN') }}
        </button>
        <button
          type="button"
          class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition-all"
          :class="region === 'global' ? 'bg-white text-emerald-800 shadow-sm dark:bg-dark-600 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-400'"
          @click="emit('update:region', 'global')"
        >
          {{ t('admin.accounts.qoderProxy.regionGlobal') }}
        </button>
      </div>
      <p class="input-hint">{{ t('admin.accounts.qoderProxy.editionHint') }}</p>
    </div>
    <div class="rounded-lg border border-emerald-200 p-3 dark:border-emerald-900">
      <div class="flex items-center justify-between gap-3">
        <label class="input-label mb-0">{{ t('admin.accounts.qoderProxy.oauth') }}</label>
        <button
          type="button"
          class="rounded-md bg-emerald-700 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-800 disabled:opacity-50"
          :disabled="loggingIn"
          @click="startLogin"
        >
          {{ loggingIn ? t('admin.accounts.qoderProxy.oauthWait') : t('admin.accounts.qoderProxy.oauthOpen') }}
        </button>
      </div>
      <p class="input-hint mt-1">{{ hint }}</p>
    </div>

    <label class="input-label">{{ t('admin.accounts.qoderProxy.personalToken') }}</label>
    <input
      :value="personalToken"
      type="password"
      class="input font-mono"
      autocomplete="new-password"
      :placeholder="mode === 'create' ? t('admin.accounts.qoderProxy.personalTokenCreatePlaceholder') : t('admin.accounts.qoderProxy.personalTokenEditPlaceholder')"
      @input="emit('update:personalToken', ($event.target as HTMLInputElement).value)"
    />
    <p class="input-hint">
      {{ t('admin.accounts.qoderProxy.personalTokenHint') }}
      <span v-if="mode === 'edit'">{{ t('admin.accounts.qoderProxy.keepHint') }}</span>
    </p>
    <label class="input-label">{{ t('admin.accounts.qoderProxy.machineId') }}</label>
    <input
      :value="machineId"
      type="text"
      class="input font-mono"
      placeholder="optional"
      @input="emit('update:machineId', ($event.target as HTMLInputElement).value)"
    />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { startQoderOAuth, pollQoderOAuth } from '@/api/admin/accounts'

export interface QoderOAuthResult {
  accessToken: string
  refreshToken: string
  userId: string
  expiresAt: string
  machineId: string
  region: 'cn' | 'global'
}

const props = defineProps<{
  mode: 'create' | 'edit'
  personalToken: string
  machineId: string
  region: 'cn' | 'global'
}>()

const emit = defineEmits<{
  'update:personalToken': [value: string]
  'update:machineId': [value: string]
  'update:region': [value: 'cn' | 'global']
  oauth: [value: QoderOAuthResult]
}>()

const { t } = useI18n()
const loggingIn = ref(false)
const hint = ref(t('admin.accounts.qoderProxy.oauthHint'))
let timer: ReturnType<typeof setInterval> | null = null

const stop = () => {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  loggingIn.value = false
}

const startLogin = async () => {
  stop()
  loggingIn.value = true
  hint.value = t('admin.accounts.qoderProxy.oauthOpening')
  try {
    const sess = await startQoderOAuth(props.region, props.machineId)
    if (sess.machine_id) emit('update:machineId', sess.machine_id)
    window.open(sess.login_url, '_blank', 'noopener')
    hint.value = t('admin.accounts.qoderProxy.oauthWaiting')
    let ticks = 0
    timer = setInterval(async () => {
      ticks += 1
      if (ticks > 90) {
        stop()
        hint.value = t('admin.accounts.qoderProxy.oauthTimeout')
        return
      }
      try {
        const result = await pollQoderOAuth(sess.region || props.region, sess.nonce, sess.verifier)
        if (result.status === 'ok' && result.access_token) {
          emit('oauth', {
            accessToken: result.access_token,
            refreshToken: result.refresh_token || '',
            userId: result.user_id || '',
            expiresAt: result.expires_at || '',
            machineId: sess.machine_id || props.machineId,
            region: (result.region === 'global' ? 'global' : 'cn')
          })
          stop()
          hint.value = t('admin.accounts.qoderProxy.oauthSuccess')
        }
      } catch {
        // Keep polling through a transient network error.
      }
    }, 2000)
  } catch (err: unknown) {
    stop()
    hint.value = err instanceof Error ? err.message : t('admin.accounts.qoderProxy.oauthFailed')
  }
}

onBeforeUnmount(stop)
</script>
