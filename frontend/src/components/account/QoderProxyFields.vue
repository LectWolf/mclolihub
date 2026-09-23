<template>
  <div class="space-y-4" data-test="qoder-proxy-fields">
    <!-- 版本：决定登录、PAT 换票与对话转发使用的站点 -->
    <div>
      <label class="input-label">{{ t('admin.accounts.qoderProxy.edition') }}</label>
      <div class="mt-1 grid grid-cols-2 gap-2" role="radiogroup" :aria-label="t('admin.accounts.qoderProxy.edition')">
        <button
          v-for="option in regionOptions"
          :key="option.value"
          type="button"
          role="radio"
          :aria-checked="region === option.value"
          :data-test="`qoder-region-${option.value}`"
          class="flex items-center gap-2.5 rounded-lg border-2 px-3 py-2 text-left transition-all disabled:cursor-not-allowed disabled:opacity-60"
          :class="
            region === option.value
              ? 'border-emerald-600 bg-emerald-50 dark:border-emerald-500 dark:bg-emerald-900/20'
              : 'border-gray-200 hover:border-emerald-300 dark:border-dark-600 dark:hover:border-emerald-700'
          "
          :disabled="loggingIn"
          @click="emit('update:region', option.value)"
        >
          <span
            class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md"
            :class="
              region === option.value
                ? 'bg-emerald-600 text-white dark:bg-emerald-500'
                : 'bg-gray-100 text-gray-500 dark:bg-dark-600 dark:text-gray-400'
            "
          >
            <Icon name="globe" size="sm" />
          </span>
          <span class="min-w-0">
            <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ option.label }}</span>
            <span class="block truncate text-xs text-gray-500 dark:text-gray-400">{{ option.host }}</span>
          </span>
          <Icon
            v-if="region === option.value"
            name="checkCircle"
            size="sm"
            class="ml-auto shrink-0 text-emerald-600 dark:text-emerald-400"
          />
        </button>
      </div>
      <p class="input-hint">{{ t('admin.accounts.qoderProxy.editionHint') }}</p>
    </div>

    <!-- 凭据：设备登录（推荐）或个人访问令牌 -->
    <div class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-600">
      <div class="p-3" data-test="qoder-device-login">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="flex min-w-0 items-start gap-2.5">
            <span
              class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300"
            >
              <Icon name="login" size="sm" />
            </span>
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.accounts.qoderProxy.deviceLogin') }}
                </span>
                <span
                  class="rounded bg-emerald-100 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300"
                >
                  {{ t('admin.accounts.qoderProxy.recommended') }}
                </span>
              </div>
              <p class="mt-0.5 text-xs" :class="statusClass" data-test="qoder-login-status">{{ statusText }}</p>
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-2">
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
              class="inline-flex items-center gap-1.5 rounded-md bg-emerald-700 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-800 disabled:opacity-50 dark:bg-emerald-600 dark:hover:bg-emerald-500"
              :disabled="loggingIn"
              data-test="qoder-login-start"
              @click="startLogin"
            >
              <svg v-if="loggingIn" class="h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z" />
              </svg>
              {{ loginButtonLabel }}
            </button>
          </div>
        </div>

        <!-- 登录进行中：倒计时 + 手动打开/复制登录链接 -->
        <div
          v-if="loggingIn && loginUrl"
          class="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-dark-700/60"
        >
          <span class="inline-flex items-center gap-1 tabular-nums text-gray-500 dark:text-gray-400">
            <Icon name="clock" size="xs" />
            {{ t('admin.accounts.qoderProxy.oauthRemaining', { time: remainingLabel }) }}
          </span>
          <a
            :href="loginUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 text-emerald-700 underline-offset-2 hover:underline dark:text-emerald-300"
          >
            <Icon name="externalLink" size="xs" />
            {{ t('admin.accounts.qoderProxy.oauthOpenLink') }}
          </a>
          <button
            type="button"
            class="inline-flex items-center gap-1 text-gray-600 hover:text-gray-900 dark:text-gray-300 dark:hover:text-white"
            @click="copyLoginUrl"
          >
            <Icon :name="copied ? 'check' : 'copy'" size="xs" />
            {{ copied ? t('admin.accounts.qoderProxy.oauthLinkCopied') : t('admin.accounts.qoderProxy.oauthCopyLink') }}
          </button>
        </div>

        <!-- 已登录 -->
        <div
          v-if="deviceLoginUser && !loggingIn"
          class="mt-2.5 flex items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2 text-xs font-medium text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200"
          data-test="qoder-logged-in"
        >
          <Icon name="checkCircle" size="sm" class="shrink-0" />
          <span class="truncate">{{ t('admin.accounts.qoderProxy.oauthLoggedIn', { user: deviceLoginUser }) }}</span>
        </div>
      </div>

      <div class="flex items-center gap-3 px-3" aria-hidden="true">
        <div class="h-px flex-1 bg-gray-200 dark:bg-dark-600" />
        <span class="text-[11px] font-medium uppercase tracking-wide text-gray-400">{{ t('admin.accounts.qoderProxy.or') }}</span>
        <div class="h-px flex-1 bg-gray-200 dark:bg-dark-600" />
      </div>

      <div class="p-3" data-test="qoder-personal-token">
        <div class="mb-2 flex items-start gap-2.5">
          <span
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-600 dark:bg-dark-600 dark:text-gray-300"
          >
            <Icon name="key" size="sm" />
          </span>
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-1.5">
              <label for="qoder-personal-token-input" class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t('admin.accounts.qoderProxy.personalToken') }}
              </label>
              <span
                v-if="mode === 'edit' && hasSavedPersonalToken"
                class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-600 dark:bg-dark-600 dark:text-gray-300"
              >
                {{ t('admin.accounts.qoderProxy.personalTokenSaved') }}
              </span>
            </div>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.qoderProxy.personalTokenHint') }}</p>
          </div>
        </div>
        <input
          id="qoder-personal-token-input"
          :value="personalToken"
          type="password"
          class="input font-mono"
          autocomplete="new-password"
          data-1p-ignore
          data-lpignore="true"
          data-bwignore="true"
          :placeholder="
            mode === 'create'
              ? t('admin.accounts.qoderProxy.personalTokenCreatePlaceholder')
              : t('admin.accounts.qoderProxy.personalTokenEditPlaceholder')
          "
          @input="emit('update:personalToken', ($event.target as HTMLInputElement).value)"
        />
        <p v-if="mode === 'edit'" class="input-hint">
          {{ t('admin.accounts.qoderProxy.keepHint') }}
          <span v-if="deviceLoginUser">{{ t('admin.accounts.qoderProxy.personalTokenReplacesLogin') }}</span>
        </p>
      </div>
    </div>

    <p
      class="flex items-start gap-1.5 text-xs"
      :class="credentialMode === 'none' ? 'text-amber-600 dark:text-amber-400' : 'text-gray-500 dark:text-gray-400'"
      data-test="qoder-credential-summary"
    >
      <Icon :name="credentialMode === 'none' ? 'exclamationTriangle' : 'infoCircle'" size="xs" class="mt-0.5 shrink-0" />
      <span>{{ credentialSummary }}</span>
    </p>

    <!-- 高级设置：机器 ID -->
    <div class="rounded-lg border border-gray-200 dark:border-dark-600">
      <button
        type="button"
        class="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-xs font-medium text-gray-600 hover:text-gray-900 dark:text-gray-300 dark:hover:text-white"
        :aria-expanded="showAdvanced"
        data-test="qoder-advanced-toggle"
        @click="showAdvanced = !showAdvanced"
      >
        <span class="inline-flex min-w-0 items-center gap-1.5">
          <Icon name="cog" size="xs" class="shrink-0" />
          {{ t('admin.accounts.qoderProxy.advanced') }}
          <span v-if="!showAdvanced && machineId" class="truncate font-mono font-normal text-gray-400">
            · {{ machineId }}
          </span>
        </span>
        <Icon name="chevronDown" size="xs" class="shrink-0 transition-transform" :class="{ 'rotate-180': showAdvanced }" />
      </button>
      <div v-if="showAdvanced" class="border-t border-gray-200 px-3 pb-3 pt-2 dark:border-dark-600">
        <label class="input-label" for="qoder-machine-id-input">{{ t('admin.accounts.qoderProxy.machineId') }}</label>
        <input
          id="qoder-machine-id-input"
          :value="machineId"
          type="text"
          class="input font-mono"
          :placeholder="t('admin.accounts.qoderProxy.machineIdPlaceholder')"
          @input="emit('update:machineId', ($event.target as HTMLInputElement).value)"
        />
        <p class="input-hint">{{ t('admin.accounts.qoderProxy.machineIdHint') }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { startQoderOAuth, pollQoderOAuth } from '@/api/admin/accounts'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'

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
type LoginPhase = 'idle' | 'opening' | 'waiting' | 'success' | 'timeout' | 'error'

const props = defineProps<{
  mode: 'create' | 'edit'
  personalToken: string
  machineId: string
  region: QoderRegion
  /** 设备登录轮询走的代理（与账号代理一致）。 */
  proxyId?: number | null
  /** 当前生效的设备登录账号；为空表示没有设备登录。 */
  deviceLoginUser?: string
  /** 编辑模式：账号已保存个人访问令牌（脱敏后只能通过 credentials_status 得知）。 */
  hasSavedPersonalToken?: boolean
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
const LOGIN_WINDOW_SECONDS = (POLL_INTERVAL_MS * POLL_MAX_ATTEMPTS) / 1000

const { t } = useI18n()
const { copied, copyToClipboard } = useClipboard()
const loggingIn = ref(false)
const loginUrl = ref('')
const phase = ref<LoginPhase>('idle')
const errorText = ref('')
const remainingSeconds = ref(0)
const showAdvanced = ref(false)
let pollTimer: ReturnType<typeof setTimeout> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null
// 每次开始/取消登录都会递增；旧轮询看到序号变化后直接丢弃结果。
let loginSeq = 0

const regionOptions = computed<{ value: QoderRegion; label: string; host: string }[]>(() => [
  { value: 'cn', label: t('admin.accounts.qoderProxy.regionCN'), host: 'qoder.com.cn' },
  { value: 'global', label: t('admin.accounts.qoderProxy.regionGlobal'), host: 'qoder.com' }
])

const statusText = computed(() => {
  switch (phase.value) {
    case 'opening':
      return t('admin.accounts.qoderProxy.oauthOpening')
    case 'waiting':
      return t('admin.accounts.qoderProxy.oauthWaiting')
    case 'success':
      return t('admin.accounts.qoderProxy.oauthSuccess')
    case 'timeout':
      return t('admin.accounts.qoderProxy.oauthTimeout')
    case 'error':
      return errorText.value || t('admin.accounts.qoderProxy.oauthFailed')
    default:
      return t('admin.accounts.qoderProxy.oauthHint')
  }
})

const statusClass = computed(() => {
  if (phase.value === 'error') return 'text-red-600 dark:text-red-400'
  if (phase.value === 'timeout') return 'text-amber-600 dark:text-amber-400'
  if (phase.value === 'success') return 'text-emerald-700 dark:text-emerald-300'
  return 'text-gray-500 dark:text-gray-400'
})

const loginButtonLabel = computed(() => {
  if (loggingIn.value) return t('admin.accounts.qoderProxy.oauthWait')
  if (props.deviceLoginUser) return t('admin.accounts.qoderProxy.oauthRelogin')
  return t('admin.accounts.qoderProxy.oauthOpen')
})

const remainingLabel = computed(() => {
  const total = Math.max(0, remainingSeconds.value)
  const minutes = Math.floor(total / 60)
  const seconds = String(total % 60).padStart(2, '0')
  return `${minutes}:${seconds}`
})

// 网关优先使用设备登录，个人访问令牌作为兜底；编辑模式下已保存的令牌留空即保留。
const credentialMode = computed<'both' | 'device' | 'pat' | 'none'>(() => {
  const device = Boolean(props.deviceLoginUser)
  const pat = Boolean(props.personalToken.trim()) || (props.mode === 'edit' && Boolean(props.hasSavedPersonalToken))
  if (device && pat) return 'both'
  if (device) return 'device'
  if (pat) return 'pat'
  return 'none'
})

const credentialSummary = computed(() => {
  switch (credentialMode.value) {
    case 'both':
      return t('admin.accounts.qoderProxy.credentialSummaryBoth')
    case 'device':
      return t('admin.accounts.qoderProxy.credentialSummaryDevice')
    case 'pat':
      return t('admin.accounts.qoderProxy.credentialSummaryPAT')
    default:
      return t('admin.accounts.qoderProxy.credentialRequired')
  }
})

const clearTimers = () => {
  if (pollTimer) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
}

const stop = () => {
  loginSeq += 1
  clearTimers()
  loggingIn.value = false
  loginUrl.value = ''
}

const cancelLogin = () => {
  stop()
  phase.value = 'idle'
}

const fail = (message: string) => {
  stop()
  errorText.value = message
  phase.value = 'error'
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

const copyLoginUrl = () => {
  if (loginUrl.value) void copyToClipboard(loginUrl.value)
}

const startCountdown = () => {
  remainingSeconds.value = LOGIN_WINDOW_SECONDS
  countdownTimer = setInterval(() => {
    remainingSeconds.value = Math.max(0, remainingSeconds.value - 1)
  }, 1000)
}

const startLogin = async () => {
  stop()
  const seq = loginSeq
  loggingIn.value = true
  errorText.value = ''
  phase.value = 'opening'
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
    phase.value = 'waiting'
    startCountdown()

    const sessionRegion: QoderRegion = sess.region === 'global' ? 'global' : 'cn'
    let attempts = 0
    const poll = async () => {
      if (seq !== loginSeq) return
      attempts += 1
      if (attempts > POLL_MAX_ATTEMPTS) {
        stop()
        phase.value = 'timeout'
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
          phase.value = 'success'
          return
        }
      } catch (err: unknown) {
        if (seq !== loginSeq) return
        if (isFatalPollError(err)) {
          fail(errorMessage(err, t('admin.accounts.qoderProxy.oauthFailed')))
          return
        }
      }
      pollTimer = setTimeout(poll, POLL_INTERVAL_MS)
    }
    pollTimer = setTimeout(poll, POLL_INTERVAL_MS)
  } catch (err: unknown) {
    popup?.close()
    if (seq !== loginSeq) return
    fail(errorMessage(err, t('admin.accounts.qoderProxy.oauthFailed')))
  }
}

onBeforeUnmount(stop)
</script>
