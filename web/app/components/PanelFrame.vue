<script setup lang="ts">
defineProps<{
  label: string
  meta?: string
}>()
</script>

<template>
  <!-- Yarı saydam yüzey: arkadaki yıldız alanı hafifçe görünsün (referans stili) -->
  <section class="overflow-hidden rounded-xl border border-line bg-surface/70 backdrop-blur-sm">
    <header class="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5">
      <h2 class="label-sys !font-mono !text-[11px] !font-normal">{{ label }}</h2>
      <div class="flex items-center gap-3">
        <span v-if="meta" class="font-mono text-[11px] text-fg-subtle">{{ meta }}</span>
        <slot name="actions" />
      </div>
    </header>
    <slot />
  </section>
</template>
