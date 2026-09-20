<template>
  <div class="space-y-3">
    <template v-if="platform === 'cursor_sand'">
      <label class="input-label">SAND_INFERENCE_RENEWAL_CREDENTIAL</label>
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
        InferenceService/Stream，走 Grok Bot / sand 额度。从沙箱 host 环境复制续期凭据，不要填 session JWT。
        <span v-if="mode === 'edit'">留空则保持原凭据。</span>
      </p>
      <label class="input-label">machine_id（可选）</label>
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
        <label class="input-label mb-0">Cursor CLI 登录</label>
        <button
          type="button"
          class="rounded-md bg-violet-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-violet-700 disabled:opacity-50"
          :disabled="cliLoggingIn"
          @click="emit('startCliLogin')"
        >
          {{ cliLoggingIn ? '等待浏览器登录…' : '打开 CLI 登录' }}
        </button>
      </div>
      <p class="input-hint">{{ cliHint }}</p>
      <label class="input-label">CLI session token</label>
      <input
        :value="sessionToken"
        type="password"
        class="input font-mono"
        autocomplete="new-password"
        :placeholder="mode === 'create' ? 'eyJ... 或点击上方 CLI 登录' : '留空保持原 token'"
        :required="mode === 'create'"
        @input="emit('update:sessionToken', ($event.target as HTMLInputElement).value)"
      />
      <p class="input-hint">
        InferenceService/RunInference，走 Cursor IDE 额度。使用 Cursor CLI（loginDeepControl / agent login）的 session JWT，不要填 grok_bot 或 Origin CLI token。
      </p>
      <label class="input-label">client_version（可选）</label>
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

const emit = defineEmits<{
  'update:renewalCredential': [value: string]
  'update:sessionToken': [value: string]
  'update:machineId': [value: string]
  'update:clientVersion': [value: string]
  startCliLogin: []
}>()
</script>
