<script setup lang="ts">
import type { VideoInfo, VideoPref } from '~/lib/screenVideo'

// Karo / video (H.264) modu secici. Ajan video pazarlamadiysa ya da tarayici
// WebCodecs ile H.264 cozemiyorsa devre disi gosterilir (karo kullanilir).
const props = defineProps<{ modelValue: VideoPref, info: VideoInfo | null }>()
const emit = defineEmits<{ 'update:modelValue': [v: VideoPref] }>()
const { t } = useI18n()

const available = computed(() => !!props.info?.available)
const usable = computed(() => available.value && !!props.info?.supported)
const note = computed(() => {
  if (!props.info) return ''
  if (!props.info.available) return t('screen.video.unavailable')
  if (!props.info.supported) return t('screen.video.unsupported')
  if (props.info.unsupported > 0) return t('screen.video.forcedTiles')
  return ''
})
const value = computed({
  get: () => props.modelValue,
  set: (v: VideoPref) => emit('update:modelValue', v),
})
</script>

<template>
  <label v-if="info && info.available" class="flex items-center gap-1.5">
    <span class="sr-only">{{ t('screen.video.label') }}</span>
    <Icon name="lucide:film" class="size-3.5" :class="info.active ? 'text-accent' : 'text-fg-subtle'" aria-hidden="true" />
    <select
      v-model="value"
      :disabled="!usable"
      class="cursor-pointer rounded border border-line bg-bg px-2 py-1.5 font-mono text-[11px] text-fg-muted focus:border-accent disabled:cursor-not-allowed disabled:opacity-60"
      :title="note || t('screen.video.hint')"
    >
      <option value="auto">{{ t('screen.video.auto') }}</option>
      <option value="tiles">{{ t('screen.video.tiles') }}</option>
      <option value="video">{{ t('screen.video.video') }}</option>
    </select>
  </label>
</template>
