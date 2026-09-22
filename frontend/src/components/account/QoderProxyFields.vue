<template>
  <div class="space-y-3">
    <label class="input-label">{{ t('admin.accounts.qoderProxy.personalToken') }}</label>
    <input
      :value="personalToken"
      type="password"
      class="input font-mono"
      autocomplete="new-password"
      :placeholder="mode === 'create' ? t('admin.accounts.qoderProxy.personalTokenCreatePlaceholder') : t('admin.accounts.qoderProxy.personalTokenEditPlaceholder')"
      :required="mode === 'create'"
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
import { useI18n } from 'vue-i18n'

defineProps<{
  mode: 'create' | 'edit'
  personalToken: string
  machineId: string
}>()

const emit = defineEmits<{
  'update:personalToken': [value: string]
  'update:machineId': [value: string]
}>()

const { t } = useI18n()
</script>
