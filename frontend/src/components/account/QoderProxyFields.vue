<template>
  <div class="space-y-3">
    <div>
      <label class="input-label">{{ t('admin.accounts.qoderProxy.edition') }}</label>
      <div class="mt-1 flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
        <button
          v-for="option in regionOptions"
          :key="option.value"
          type="button"
          class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition-all disabled:cursor-not-allowed disabled:opacity-60"
          :class="region === option.value ? 'bg-white text-emerald-800 shadow-sm dark:bg-dark-600 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-400'"
          :disabled="loggingIn"
          @click="emit('update:region', option.value)"
        >
          {{ option.label }}
        </button>
      </div>
      <p class="input-hint">{{ t('admin.accounts.qoderProxy.editionHint') }}</p>
    </div>

    <div class="rounded-lg border border-emerald-200 p-3 dark:border-emerald-900">
      <div class="flex items-center justify-between gap-3">
        <label class="input-label mb-0">{{ t('admin.accounts.qoderProxy.oauth') }}</label>
        <div class="flex items-center gap-2">
          <button
            v-if="loggingIn"
            type="button"
            class="rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50 dark:border-dark-500 dark:text-gray-300 dark:hover:bg-dark-600"
            @click="cancelLogin"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="rounded-md bg-emerald-700 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-800 disabled:opacity-50"
            :disabled="loggingIn"
            @click="startLogin"
          >
            {{ loggingIn ? t('admin.accounts.qoderProxy.oauthWait') : t('admin.accounts.qoderProxy.oauthOpen') }}
          </button>
        </div>
      </div>
      <p
        v-if="deviceLoginUser && !loggingIn"
        class="mt-1 text-xs font-medium text-emerald-700 dark:text-emerald-300"
      >
        {{ t('admin.accounts.qoderProxy.oauthLoggedIn', { user: deviceLoginUser }) }}
      </p>
      <p class="input-hint mt-1">{{ hint }}</p>
      <a
        v-if="loggingIn && loginUrl"
        :href="loginUrl"
        target="_blank"
        rel="noopener noreferrer"
        class="mt-1 inline-block break-all text-xs text-emerald-700 underline hover:text-emerald-800 dark:text-emerald-300"
      >
        {{ t('admin.accounts.qoderProxy.oauthOpenLink') }}
      </a>
    </div>

    <div>
      <label class="input-label">{{ t('admin.accounts.qoderProxy.personalToken') }}</label>
      <input
        :value="personalToken"
        type="password"
        class="input font-mono"
        autocomplete="new-password"
        data-1p-ignore
        data-lpignore="true"
        data-bwignore="true"
        :placeholder="mode === 'create' ? t('admin.accounts.qoderProxy.personalTokenCreatePlaceholder') : t('admin.accounts.qoderProxy.personalTokenEditPlaceholder')"
        @input="emit('update:personalToken', ($event.target as HTMLInputElement).value)"
      />
      <p class="input-hint">
        {{ t('admin.accounts.qoderProxy.personalTokenHint') }}
        <span v-if="mode === 'edit'">{{ t('admin.accounts.qoderProxy.keepHint') }}</span>
        <span v-if="mode === 'edit' && deviceLoginUser">{{ t('admin.accounts.qoderProxy.personalTokenReplacesLogin') }}</span>
      </p>
    </div>

    <div>
      <label class="input-label">{{ t('admin.accounts.qoderProxy.machineId') }}</label>
      <input
        :value="machineId"
        type="text"
        class="input font-mono"
        :placeholder="t('admin.accounts.qoderProxy.machineIdPlaceholder')"
        @input="emit('update:machineId', ($event.target as HTMLInputElement).value)"
      />
      <p class="input-hint">{{ t('admin.accounts.qoderProxy.machineIdHint') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { startQoderOAuth, pollQoderOAuth } from '@/api/admin/accounts'

export interface QoderOAuthResult {
  accessToken: string
  refreshToken: string
  userId: string
  name: string
  email: string
  expiresAt: string
  machineId: string
  region: 'cn' | 'global'
}

type QoderRegion = 'cn' | 'global'

const props = defineProps<{
  mode: 'create' | 'edit'
  personalToken: string
  machineId: string
  region: QoderRegion
  /** 设备登录轮询走的代理（与账号代理一致）。 */
  proxyId?: number | null
  /** 当前生效的设备登录账号；为空表示没有设备登录。 */
  deviceLoginUser?: string
}>()

const emit = defineEmits<{
  'update:personalToken': [value: string]
  'update:machineId': [value: string]
  'update:region': [value: QoderRegion]
  oauth: [value: QoderOAuthResult]
}>()

// 设备登录页一般 1~2 分钟内完成；轮询间隔 2 秒，最长约 5 分钟。
const POLL_INTERVAL_MS = 2000
const POLL_MAX_ATTEMPTS = 150

const { t } = useI18n()
const loggingIn = ref(false)
const loginUrl = ref('')
const hint = ref(t('admin.accounts.qoderProxy.oauthHint'))
let pollTimer: ReturnType<typeof setTimeout> | null = null
// 每次开始/取消登录都会递增；旧轮询看到序号变化后直接丢弃结果。
let loginSeq = 0

const regionOptions = computed<{ value: QoderRegion; label: string }[]>(() => [
  { value: 'cn', label: t('admin.accounts.qoderProxy.regionCN') },
  { value: 'global', label: t('admin.accounts.qoderProxy.regionGlobal') }
])

const stop = () => {
  loginSeq += 1
  if (pollTimer) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
  loggingIn.value = false
  loginUrl.value = ''
}

const cancelLogin = () => {
  stop()
  hint.value = t('admin.accounts.qoderProxy.oauthHint')
}

const errorMessage = (err: unknown, fallback: string): string => {
  if (err && typeof err === 'object' && 'message' in err) {
    const message = String((err as { message?: unknown }).message || '').trim()
    if (message) return message
  }
  return fallback
}

// 400 等明确的业务错误（登录被拒、会话失效）不会自愈，停止轮询；网络错误与 5xx 继续重试。
const isFatalPollError = (err: unknown): boolean => {
  const status = err && typeof err === 'object' && 'status' in err ? Number((err as { status?: unknown }).status) : 0
  return status >= 400 && status < 500
}

// 先同步打开空白窗口，避免异步请求返回后再 window.open 被浏览器当作弹窗拦截。
const openBlankWindow = (): Window | null => {
  const popup = window.open('about:blank', '_blank')
  if (popup) popup.opener = null
  return popup
}

const startLogin = async () => {
  stop()
  const seq = loginSeq
  loggingIn.value = true
  hint.value = t('admin.accounts.qoderProxy.oauthOpening')
  const popup = openBlankWindow()
  try {
    const sess = await startQoderOAuth(props.region, props.machineId)
    if (seq !== loginSeq) {
      popup?.close()
      return
    }
    const machineId = sess.machine_id || props.machineId
    if (sess.machine_id) emit('update:machineId', sess.machine_id)
    loginUrl.value = sess.login_url
    if (popup && !popup.closed) {
      popup.location.href = sess.login_url
    } else {
      window.open(sess.login_url, '_blank', 'noopener')
    }
    hint.value = t('admin.accounts.qoderProxy.oauthWaiting')

    const sessionRegion: QoderRegion = sess.region === 'global' ? 'global' : 'cn'
    let attempts = 0
    const poll = async () => {
      if (seq !== loginSeq) return
      attempts += 1
      if (attempts > POLL_MAX_ATTEMPTS) {
        stop()
        hint.value = t('admin.accounts.qoderProxy.oauthTimeout')
        return
      }
      try {
        const result = await pollQoderOAuth(sessionRegion, sess.nonce, sess.verifier, props.proxyId)
        if (seq !== loginSeq) return
        if (result.status === 'ok' && result.access_token) {
          stop()
          emit('oauth', {
            accessToken: result.access_token,
            refreshToken: result.refresh_token || '',
            userId: result.user_id || '',
            name: result.name || '',
            email: result.email || '',
            expiresAt: result.expires_at || '',
            machineId,
            region: result.region === 'global' ? 'global' : sessionRegion
          })
          hint.value = t('admin.accounts.qoderProxy.oauthSuccess')
          return
        }
      } catch (err: unknown) {
        if (seq !== loginSeq) return
        if (isFatalPollError(err)) {
          stop()
          hint.value = errorMessage(err, t('admin.accounts.qoderProxy.oauthFailed'))
          return
        }
      }
      pollTimer = setTimeout(poll, POLL_INTERVAL_MS)
    }
    pollTimer = setTimeout(poll, POLL_INTERVAL_MS)
  } catch (err: unknown) {
    popup?.close()
    if (seq !== loginSeq) return
    stop()
    hint.value = errorMessage(err, t('admin.accounts.qoderProxy.oauthFailed'))
  }
}

onBeforeUnmount(stop)
</script>
