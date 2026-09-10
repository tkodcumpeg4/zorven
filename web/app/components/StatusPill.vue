<script setup lang="ts">
const props = defineProps<{
  status: 'online' | 'offline' | 'enabled' | 'disabled'
}>()

const { t } = useI18n()
const map = {
  online:   { text: t('status.online'), dot: 'bg-accent',      fg: 'text-accent',    pulse: true },
  offline:  { text: t('status.offline'), dot: 'bg-fg-subtle',  fg: 'text-fg-subtle', pulse: false },
  enabled:  { text: t('status.enabled'), dot: 'bg-accent',      fg: 'text-accent',    pulse: false },
  disabled: { text: t('status.disabled'), dot: 'bg-warn',        fg: 'text-warn',      pulse: false },
}

const s = computed(() => map[props.status])
</script>

<template>
  <span class="inline-flex items-center gap-1.5 font-mono text-[11px]" :class="s.fg">
    <span class="relative grid size-2 place-items-center">
      <span
        v-if="s.pulse"
        class="absolute size-2 animate-ping rounded-full opacity-60"
        :class="s.dot"
      />
      <span class="size-1.5 rounded-full" :class="s.dot" />
    </span>
    {{ s.text }}
  </span>
</template>
