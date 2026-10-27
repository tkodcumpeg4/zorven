<script setup lang="ts">
// Ingress'in proxy'lemeden reddettigi isteklerin kucuk rozeti; ipucu (tooltip)
// ret nedenini gosterir. reason bossa hicbir sey cizilmez.
const props = defineProps<{ reason?: string }>()
const { t, te } = useI18n()

const label = computed(() => {
  const r = props.reason || ''
  // ZTNA Z3: dinamik nedenler (waf:<kural>, waf_log:<kural>, geo:<ulke>)
  if (r.startsWith('waf_log:')) return t('tunsec.reasonWafLog', { rule: r.slice(8) })
  if (r.startsWith('waf:')) return t('tunsec.reasonWaf', { rule: r.slice(4) })
  if (r.startsWith('geo:')) return t('tunsec.reasonGeo', { country: r.slice(4) })
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
