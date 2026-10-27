<script setup lang="ts">
import type { AgentStats } from '~/lib/screenPerf'
import type { VideoInfo } from '~/lib/screenVideo'
import { fmtMs } from '~/lib/screenPerf'

// Performans katmaninin video bolumu: mod (karo/video), bit hizi, cozme suresi.
// Ajan video pazarlamadiysa hic gorunmez.
const props = defineProps<{ info: VideoInfo | null, agent: AgentStats | null }>()
const { t } = useI18n()

const visible = computed(() => !!props.info?.available)
const showing = computed(() => (props.info?.active ? t('screen.video.modeVideo') : t('screen.video.modeTiles')))
const kbit = computed(() => (props.info?.active ? props.info.kbps : Math.round((props.agent?.kbps ?? 0) * 8)))
</script>

<template>
  <template v-if="visible && info">
    <p class="mb-1 text-white/50">{{ t('screen.video.perfTitle') }}</p>
    <dl class="mb-2 grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5">
      <dt class="text-white/60">{{ t('screen.video.mode') }}</dt>
      <dd :class="info.active ? 'text-emerald-300' : ''">{{ showing }}<span class="text-white/50"> ({{ info.effective }})</span></dd>
      <dt class="text-white/60">{{ t('screen.video.rate') }}</dt>
      <dd>{{ kbit }} kbit/s · {{ info.active ? info.fps : (agent?.fps ?? 0) }} fps</dd>
      <template v-if="info.active">
        <dt class="text-white/60">{{ t('screen.video.decode') }}</dt>
        <dd>{{ fmtMs(info.decodeMs) }}</dd>
        <dt class="text-white/60">{{ t('screen.video.codec') }}</dt>
        <dd>{{ info.codec }} {{ info.w }}×{{ info.h }}</dd>
      </template>
      <template v-if="agent && agent.video_bitrate">
        <dt class="text-white/60">{{ t('screen.video.agentTarget') }}</dt>
        <dd>{{ Math.round(agent.video_bitrate / 1000) }} kbit/s<span v-if="agent.backend" class="text-white/50"> · {{ agent.backend }}</span></dd>
      </template>
    </dl>
  </template>
</template>
