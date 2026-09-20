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
        :required="mode === 'create'"
        @input="emit('update:renewalCredential', ($event.target as HTMLInputElement).value)"
      />
      <p class="input-hint">
        {{ t('admin.accounts.cursorProxy.sandCredentialHint') }}
        <span v-if="mode === 'edit'">{{ t('admin.accounts.cursorProxy.sandKeepHint') }}</span>
      </p>
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
import { useI18n } from 'vue-i18n'

defineProps<{
  platform: 'cursor_sand' | 'cursor'
  mode: 'create' | 'edit'
  renewalCredential: string
  sessionToken: string
  machineId: string
  clientVersion: string
  cliLoggingIn: boolean
  cliHint: string
}>()

const { t } = useI18n()

const emit = defineEmits<{
  'update:renewalCredential': [value: string]
  'update:sessionToken': [value: string]
  'update:machineId': [value: string]
  'update:clientVersion': [value: string]
  startCliLogin: []
}>()
</script>
