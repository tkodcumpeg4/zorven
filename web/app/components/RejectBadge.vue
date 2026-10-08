<script setup lang="ts">
// Ingress'in proxy'lemeden reddettigi isteklerin kucuk rozeti; ipucu (tooltip)
// ret nedenini gosterir. reason bossa hicbir sey cizilmez.
const props = defineProps<{ reason?: string }>()
const { t, te } = useI18n()

const label = computed(() => {
  const key = `requests.reject.${props.reason}`
  return te(key) ? t(key) : (props.reason || '')
})
</script>

<template>
  <span
    v-if="reason"
    class="inline-flex shrink-0 items-center gap-1 rounded border border-warn/40 bg-warn/10 px-1 py-px font-mono text-[10px] text-warn"
    :title="label"
    :aria-label="`${t('requests.rejectedBadge')}: ${label}`"
  >
    <Icon name="lucide:shield-off" class="size-3" />
    {{ t('requests.rejectedBadge') }}
  </span>
</template>
