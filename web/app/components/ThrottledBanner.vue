<script setup lang="ts">
const { isThrottled, currentPlan, openUpgrade } = useBilling()
const { t } = useI18n()
const dismissed = ref(false)

const throttledSpeed = computed(() => {
  return currentPlan.value?.bandwidth_throttled_mbps || 1
})

const normalSpeed = computed(() => {
  return currentPlan.value?.bandwidth_normal_mbps || 10
})
</script>

<template>
  <div
    v-if="isThrottled && !dismissed"
    class="border-b px-4 py-2.5 backdrop-blur-md
           border-amber-300 bg-amber-50 text-amber-950
           dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
    role="alert"
  >
    <div class="mx-auto flex max-w-6xl items-center justify-between gap-4">
      <div class="flex items-center gap-2.5">
        <span class="grid size-6 place-items-center rounded-full bg-amber-200 text-amber-800 dark:bg-amber-500/20 dark:text-amber-400">
          <Icon name="lucide:gauge" class="size-3.5" aria-hidden="true" />
        </span>
        <div class="text-xs text-amber-950 dark:text-amber-200">
          <span class="font-bold text-amber-900 dark:text-amber-300">{{ t('throttle.label') }}</span>
          {{ t('throttle.bodyPre') }}
          <span class="font-mono font-bold text-fg">{{ normalSpeed }} Mbps</span> {{ t('throttle.bodyMid') }}
          <span class="font-mono font-bold text-amber-800 dark:text-amber-300">{{ throttledSpeed }} Mbps</span>
          {{ t('throttle.bodyPost') }}
        </div>
      </div>

      <div class="flex items-center gap-2">
        <button
          type="button"
          class="cursor-pointer rounded-lg px-2.5 py-1 text-xs font-semibold transition-colors
                 border border-amber-300 bg-amber-100 text-amber-900 hover:bg-amber-200
                 dark:border-amber-500/40 dark:bg-amber-500/20 dark:text-amber-200 dark:hover:bg-amber-500/30"
          @click="openUpgrade(t('throttle.upgradeMsg'))"
        >
          {{ t('throttle.upgradeSpeed') }}
        </button>
        <button
          type="button"
          class="text-amber-800 hover:text-amber-950 dark:text-amber-400/60 dark:hover:text-amber-300 cursor-pointer"
          :title="t('common.close')"
          @click="dismissed = true"
        >
          <Icon name="lucide:x" class="size-4" />
        </button>
      </div>
    </div>
  </div>
</template>
