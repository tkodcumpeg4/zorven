<script setup lang="ts">
// Tam ağ katmanını (Mod B) açma yardımı: katlanır kart, kopyalanabilir komutlar.
const { t } = useI18n()
const open = ref(false)
const copied = ref('')

const cmds = ['sudo zorven net up', 'zorven net up', 'zorven net status', 'zorven net down']

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = text
    setTimeout(() => { if (copied.value === text) copied.value = '' }, 1500)
  } catch { /* pano erişimi yok */ }
}
</script>

<template>
  <section class="rounded-xl border border-line bg-surface p-5">
    <button type="button" class="flex w-full cursor-pointer items-center justify-between gap-3 text-left" :aria-expanded="open" @click="open = !open">
      <span class="flex items-center gap-2 text-sm font-semibold text-fg">
        <Icon name="lucide:circle-help" class="size-4 text-fg-muted" />
        {{ t('privnet.howto.title') }}
      </span>
      <Icon name="lucide:chevron-down" class="size-4 text-fg-muted transition-transform" :class="open ? 'rotate-180' : ''" />
    </button>

    <div v-if="open" class="mt-4 space-y-4 text-xs leading-relaxed text-fg-muted">
      <p>{{ t('privnet.howto.what') }}</p>

      <div>
        <div class="mb-1.5 flex items-center gap-1.5 font-medium text-fg">
          <Icon name="lucide:terminal" class="size-3.5" />{{ t('privnet.howto.cliTitle') }}
        </div>
        <ul class="space-y-1.5">
          <li v-for="(c, i) in cmds" :key="c" class="flex items-center justify-between gap-2 rounded-lg border border-line bg-bg px-3 py-2">
            <span class="min-w-0">
              <code class="font-mono text-fg">{{ c }}</code>
              <span class="ml-2 text-fg-subtle">{{ t('privnet.howto.cmd' + i) }}</span>
            </span>
            <button type="button" class="inline-flex shrink-0 cursor-pointer items-center gap-1 font-mono text-fg-muted hover:text-fg" :title="t('privnet.howto.copy')" @click="copy(c)">
              <Icon :name="copied === c ? 'lucide:check' : 'lucide:copy'" class="size-3" />
            </button>
          </li>
        </ul>
        <p class="mt-2">{{ t('privnet.howto.cliNote') }}</p>
      </div>

      <div>
        <div class="mb-1.5 flex items-center gap-1.5 font-medium text-fg">
          <Icon name="lucide:monitor" class="size-3.5" />{{ t('privnet.howto.desktopTitle') }}
        </div>
        <p>{{ t('privnet.howto.desktop') }}</p>
      </div>

      <p class="flex items-start gap-1.5">
        <Icon name="lucide:shield-check" class="mt-px size-3.5 shrink-0" />
        <span>{{ t('privnet.howto.driver') }}</span>
      </p>
      <p class="flex items-start gap-1.5">
        <Icon name="lucide:lock" class="mt-px size-3.5 shrink-0" />
        <span>{{ t('privnet.howto.access') }}</span>
      </p>
    </div>
  </section>
</template>
