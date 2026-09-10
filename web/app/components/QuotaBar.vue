<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    label: string
    used: number
    limit: number | null
    customUsedText?: string
    customLimitText?: string
    unit?: string
    showPercent?: boolean
  }>(),
  {
    customUsedText: '',
    customLimitText: '',
    unit: '',
    showPercent: true,
  },
)

const { t } = useI18n()
const isUnlimited = computed(() => props.limit === null || props.limit === undefined)

const percentage = computed(() => {
  if (isUnlimited.value || !props.limit || props.limit <= 0) return 0
  return Math.min(Math.round((props.used / props.limit) * 100), 100)
})

const barColor = computed(() => {
  if (isUnlimited.value) return 'bg-accent/40'
  if (percentage.value >= 90) return 'bg-rose-500'
  if (percentage.value >= 75) return 'bg-amber-500'
  return 'bg-accent'
})

const textColor = computed(() => {
  if (isUnlimited.value) return 'text-fg-muted'
  if (percentage.value >= 90) return 'text-rose-400 font-semibold'
  if (percentage.value >= 75) return 'text-amber-400'
  return 'text-fg'
})
</script>

<template>
  <div class="space-y-1.5">
    <div class="flex items-center justify-between text-xs">
      <span class="text-fg-muted font-medium">{{ label }}</span>
      <div class="font-mono text-[11px]" :class="textColor">
        <template v-if="isUnlimited">
          <span>{{ customUsedText || used }}</span>
          <span class="text-fg-subtle"> / {{ t('common.unlimited') }}</span>
        </template>
        <template v-else>
          <span>{{ customUsedText || used }}</span>
          <span class="text-fg-subtle"> / {{ customLimitText || limit }}</span>
          <span v-if="unit" class="text-fg-subtle ml-0.5">{{ unit }}</span>
          <span v-if="showPercent" class="text-fg-subtle ml-1.5 font-sans text-[10px]">
            ({{ percentage }}%)
          </span>
        </template>
      </div>
    </div>

    <!-- Progress Bar Track -->
    <div class="h-1.5 w-full overflow-hidden rounded-full bg-line/60">
      <div
        class="h-full rounded-full transition-all duration-500 ease-out"
        :class="barColor"
        :style="{ width: isUnlimited ? '15%' : `${percentage}%` }"
      />
    </div>
  </div>
</template>
