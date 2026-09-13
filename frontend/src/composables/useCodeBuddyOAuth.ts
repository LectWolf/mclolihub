import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'

export function useCodeBuddyOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const loginId = ref('')
  const verificationUri = ref('')
  const loading = ref(false)
  const polling = ref(false)
  const error = ref('')
  const done = ref(false)
  const uid = ref('')
  const nickname = ref('')
  let pollTimer: ReturnType<typeof setInterval> | null = null

  const resetState = () => {
    stopPolling()
    loginId.value = ''
    verificationUri.value = ''
    loading.value = false
    polling.value = false
    error.value = ''
    done.value = false
    uid.value = ''
    nickname.value = ''
  }

  const stopPolling = () => {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
    polling.value = false
  }

  const start = async (site: 'cn' | 'intl', proxyId?: number | null) => {
    loading.value = true
    error.value = ''
    done.value = false
    try {
      const payload: Record<string, unknown> = { site }
      if (proxyId) payload.proxy_id = proxyId
      const result = await adminAPI.codebuddy.startLogin(payload as any)
      loginId.value = result.login_id
      verificationUri.value = result.verification_uri
      startPolling()
      return true
    } catch (err: any) {
      error.value = extractApiErrorMessage(err, t('admin.accounts.codebuddyOAuth.failedToStart'))
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const startPolling = () => {
    stopPolling()
    polling.value = true
    pollTimer = setInterval(async () => {
      if (!loginId.value) return
      try {
        const result = await adminAPI.codebuddy.pollLogin(loginId.value)
        if (!result.done) return
        stopPolling()
        if (result.error) {
          error.value = result.error
          appStore.showError(result.error)
          return
        }
        done.value = true
        uid.value = result.uid || ''
        nickname.value = result.nickname || ''
      } catch (err: any) {
        error.value = extractApiErrorMessage(err, t('admin.accounts.codebuddyOAuth.failedToPoll'))
      }
    }, 2000)
  }

  return {
    loginId,
    verificationUri,
    loading,
    polling,
    error,
    done,
    uid,
    nickname,
    resetState,
    stopPolling,
    start
  }
}
