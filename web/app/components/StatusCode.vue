<script setup lang="ts">
const props = defineProps<{ code: number }>()

const tone = computed(() => {
  if (props.code >= 500) return 'text-danger'
  if (props.code >= 400) return 'text-warn'
  if (props.code >= 300) return 'text-fg-muted'
  return 'text-accent'
})
</script>

<template>
  <span class="font-mono text-xs tabular-nums" :class="tone">{{ code }}</span>
</template>
