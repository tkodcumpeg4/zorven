<script setup lang="ts">
const props = defineProps<{ method: string }>()

const tone = computed(() => ({
  GET:    'text-info',
  POST:   'text-accent',
  PUT:    'text-warn',
  PATCH:  'text-warn',
  DELETE: 'text-danger',
}[props.method] ?? 'text-fg-muted'))
</script>

<template>
  <span class="font-mono text-[11px] font-semibold" :class="tone">{{ method }}</span>
</template>
