<script setup lang="ts">
defineProps<{
  label: string
  value: string | number
  index?: string
  hint?: string
  /** 'accent' = referanstaki canlı gradient blok muamelesi */
  tone?: 'default' | 'accent' | 'danger'
}>()
</script>

<template>
  <div
    class="relative overflow-hidden rounded-xl px-4 py-3.5"
    :class="tone === 'accent'
      ? 'surface-accent'
      : 'border border-line bg-surface/70 backdrop-blur-sm'"
  >
    <span
      v-if="index"
      class="absolute right-3 top-3 font-mono text-[11px]"
      :class="tone === 'accent' ? 'text-white/40' : 'text-fg-subtle/50'"
      aria-hidden="true"
    >{{ index }}</span>

    <p
      class="label-sys"
      :class="tone === 'accent' ? '!text-white/70' : ''"
    >{{ label }}</p>

    <p
      class="mt-1.5 font-mono text-2xl font-semibold tabular-nums"
      :class="{
        'text-fg': !tone || tone === 'default',
        'text-white': tone === 'accent',
        'text-danger': tone === 'danger',
      }"
    >{{ value }}</p>

    <p
      v-if="hint"
      class="mt-0.5 text-xs"
      :class="tone === 'accent' ? 'text-white/70' : 'text-fg-muted'"
    >{{ hint }}</p>
  </div>
</template>
