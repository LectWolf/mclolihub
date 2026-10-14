<template>
  <div class="flex flex-wrap items-center gap-2" role="group" :aria-label="label">
    <span class="mr-1 inline-flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
      <Icon name="filter" size="xs" />
      {{ label }}
    </span>
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      class="inline-flex max-w-full items-center gap-2 rounded-full border px-3 py-1.5 text-xs font-semibold outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:focus-visible:ring-offset-dark-900"
      :class="modelValue === option.value
        ? 'border-transparent bg-primary-600 text-white shadow-sm'
        : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-200 dark:hover:bg-dark-700'"
      :aria-pressed="modelValue === option.value"
      :title="option.label"
      :data-platform-filter="kind === 'platform' ? option.value : undefined"
      :data-group-filter="kind === 'group' ? option.value : undefined"
      @click="$emit('update:modelValue', option.value)"
    >
      <span
        class="grid h-5 w-5 shrink-0 place-items-center rounded-full"
        :class="modelValue === option.value ? 'bg-white/20' : 'bg-gray-100 dark:bg-dark-700'"
      >
        <ProviderIcon v-if="option.platform" :provider="option.platform" :size="13" />
        <Icon v-else name="grid" size="xs" />
      </span>
      <span class="max-w-[220px] truncate">{{ option.label }}</span>
      <span
        class="rounded-full px-1.5 py-px font-mono text-[10px] tabular-nums"
        :class="modelValue === option.value ? 'bg-white/20 text-white' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'"
      >{{ option.count }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'

export interface MonitorFilterChip {
  value: string
  label: string
  count: number
  platform?: string
}

defineProps<{
  kind: 'platform' | 'group'
  label: string
  options: MonitorFilterChip[]
  modelValue: string
}>()
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>
