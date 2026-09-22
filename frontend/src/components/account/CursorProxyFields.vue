<template>
  <div class="space-y-3">
    <template v-if="platform === 'cursor_sand'">
      <label class="input-label">{{ t('admin.accounts.cursorProxy.sandCredential') }}</label>
      <input
        :value="renewalCredential"
        type="password"
        class="input font-mono"
        autocomplete="new-password"
        placeholder="sbi_..."
        @input="emit('update:renewalCredential', ($event.target as HTMLInputElement).value)"
      />
      <p class="input-hint">
        {{ t('admin.accounts.cursorProxy.sandCredentialHint') }}
        <span v-if="mode === 'edit'">{{ t('admin.accounts.cursorProxy.sandKeepHint') }}</span>
      </p>

      <label class="input-label">{{ t('admin.accounts.cursorProxy.grokBotToken') }}</label>
      <input
        :value="grokBotToken"
        type="password"
        class="input font-mono"
        autocomplete="new-password"
        placeholder="eyJ..."
        @input="emit('update:grokBotToken', ($event.target as HTMLInputElement).value)"
      />
      <p class="input-hint">{{ t('admin.accounts.cursorProxy.grokBotTokenHint') }}</p>

      <div class="rounded-lg border border-zinc-200 p-3 dark:border-dark-600">
        <div class="flex items-center justify-between gap-3">
          <label class="input-label mb-0">{{ t('admin.accounts.cursorProxy.grokOAuth') }}</label>
          <button
            type="button"
            class="rounded-md bg-zinc-800 px-3 py-1.5 text-xs font-medium text-white hover:bg-zinc-900 disabled:opacity-50 dark:bg-zinc-600"
            :disabled="grokOAuthLoading"
            @click="startGrokOAuth"
          >
            {{ grokOAuthLoading ? t('admin.accounts.cursorProxy.grokOAuthWait') : t('admin.accounts.cursorProxy.grokOAuthOpen') }}
          </button>
        </div>
        <p class="input-hint mt-1">{{ grokOAuthHint }}</p>
        <div v-if="grokAuthUrl" class="mt-2 space-y-2">
          <input
            v-model="grokAuthCode"
            type="text"
            class="input font-mono"
            :placeholder="t('admin.accounts.cursorProxy.grokOAuthCodePlaceholder')"
          />
          <button
            type="button"
            class="rounded-md bg-zinc-700 px-3 py-1.5 text-xs font-medium text-white hover:bg-zinc-800 disabled:opacity-50"
            :disabled="!grokAuthCode.trim() || grokOAuthLoading"
            @click="finishGrokOAuth"
          >
            {{ t('admin.accounts.cursorProxy.grokOAuthExchange') }}
          </button>
        </div>
      </div>

      <label class="input-label">{{ t('admin.accounts.cursorProxy.machineId') }}</label>
      <input
        :value="machineId"
        type="text"
        class="input font-mono"
        placeholder="SAND_BOX_STORE_ID"
        @input="emit('update:machineId', ($event.target as HTMLInputElement).value)"
      />
    </template>

    <template v-else>
      <div class="flex items-center justify-between gap-3">
        <label class="input-label mb-0">{{ t('admin.accounts.cursorProxy.cliLogin') }}</label>
        <button
          type="button"
          class="rounded-md bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-700 disabled:opacity-50"
          :disabled="cliLoggingIn"
          @click="emit('startCliLogin')"
        >
          {{ cliLoggingIn ? t('admin.accounts.cursorProxy.cliLoginWait') : t('admin.accounts.cursorProxy.cliLoginOpen') }}
        </button>
      </div>
      <p class="input-hint">{{ cliHint }}</p>
      <label class="input-label">{{ t('admin.accounts.cursorProxy.sessionToken') }}</label>
      <input
        :value="sessionToken"
        type="password"
        class="input font-mono"
        autocomplete="new-password"
        :placeholder="mode === 'create' ? t('admin.accounts.cursorProxy.sessionTokenCreatePlaceholder') : t('admin.accounts.cursorProxy.sessionTokenEditPlaceholder')"
        :required="mode === 'create'"
        @input="emit('update:sessionToken', ($event.target as HTMLInputElement).value)"
      />
      <p class="input-hint">
        {{ t('admin.accounts.cursorProxy.sessionTokenHint') }}
      </p>
      <label class="input-label">{{ t('admin.accounts.cursorProxy.clientVersion') }}</label>
      <input
        :value="clientVersion"
        type="text"
        class="input font-mono"
        placeholder="3.21.12"
        @input="emit('update:clientVersion', ($event.target as HTMLInputElement).value)"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useGrokOAuth } from '@/composables/useGrokOAuth'

const props = defineProps<{
  platform: 'cursor_sand' | 'cursor'
  mode: 'create' | 'edit'
  renewalCredential: string
  grokBotToken: string
  sessionToken: string
  machineId: string
  clientVersion: string
  cliLoggingIn: boolean
  cliHint: string
  proxyId?: number | null
}>()

const emit = defineEmits<{
  'update:renewalCredential': [value: string]
  'update:grokBotToken': [value: string]
  'update:sessionToken': [value: string]
  'update:machineId': [value: string]
  'update:clientVersion': [value: string]
  startCliLogin: []
  grokOAuthComplete: [payload: { accessToken: string; refreshToken?: string }]
}>()

const { t } = useI18n()
const grokOAuth = useGrokOAuth()
const grokOAuthLoading = grokOAuth.loading
const grokAuthUrl = grokOAuth.authUrl
const grokAuthCode = ref('')
const grokOAuthHint = ref('')

const startGrokOAuth = async () => {
  grokOAuthHint.value = t('admin.accounts.cursorProxy.grokOAuthOpening')
  const ok = await grokOAuth.generateAuthUrl(props.proxyId)
  if (!ok || !grokOAuth.authUrl.value) {
    grokOAuthHint.value = grokOAuth.error.value || t('admin.accounts.cursorProxy.grokOAuthFailed')
    return
  }
  window.open(grokOAuth.authUrl.value, '_blank', 'noopener')
  grokOAuthHint.value = t('admin.accounts.cursorProxy.grokOAuthPasteCode')
}

const finishGrokOAuth = async () => {
  const info = await grokOAuth.exchangeAuthCode({
    code: grokAuthCode.value,
    sessionId: grokOAuth.sessionId.value,
    state: grokOAuth.state.value,
    proxyId: props.proxyId
  })
  if (!info?.access_token) {
    grokOAuthHint.value = grokOAuth.error.value || t('admin.accounts.cursorProxy.grokOAuthFailed')
    return
  }
  emit('update:grokBotToken', info.access_token)
  emit('grokOAuthComplete', {
    accessToken: info.access_token,
    refreshToken: info.refresh_token
  })
  grokAuthCode.value = ''
  grokOAuthHint.value = info.email
    ? t('admin.accounts.cursorProxy.grokOAuthSuccessEmail', { email: info.email })
    : t('admin.accounts.cursorProxy.grokOAuthSuccess')
}
</script>
