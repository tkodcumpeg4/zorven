<script setup lang="ts">
// MCP baglanti karti: uc nokta adresi, gereken kapsamlar ve kopyalanabilir
// yapilandirma ornekleri (Claude Code uzak HTTP, Claude Desktop/Cursor stdio).
const { t } = useI18n()
const toast = useToast()
const origin = useRequestURL().origin
const endpoint = `${origin}/mcp`
const copiedKey = ref('')

const snippets = computed(() => [
  {
    key: 'claudeCode',
    title: t('tokens.mcp.claudeCode'),
    code: `claude mcp add --transport http zorven ${endpoint} --header "Authorization: Bearer YOUR_API_KEY"`,
  },
  {
    key: 'desktop',
    title: t('tokens.mcp.claudeDesktop'),
    code: JSON.stringify({
      mcpServers: {
        zorven: { command: 'zorven', args: ['mcp'], env: { ZORVEN_API_TOKEN: 'YOUR_API_KEY' } },
      },
    }, null, 2),
  },
  {
    key: 'cursor',
    title: t('tokens.mcp.cursor'),
    code: JSON.stringify({
      mcpServers: {
        zorven: { url: endpoint, headers: { Authorization: 'Bearer YOUR_API_KEY' } },
      },
    }, null, 2),
  },
])

function copy(key: string, text: string) {
  navigator.clipboard.writeText(text)
  copiedKey.value = key
  toast.success(t('common.copied'))
  setTimeout(() => { if (copiedKey.value === key) copiedKey.value = '' }, 2000)
}
</script>

<template>
  <section class="rounded-xl border border-line bg-surface p-5">
    <div class="mb-3 flex items-center gap-2">
      <Icon name="lucide:plug-zap" class="size-4 text-accent" />
      <h2 class="text-sm font-semibold text-fg">{{ t('tokens.mcp.title') }}</h2>
    </div>
    <p class="mb-4 text-xs leading-relaxed text-fg-muted">{{ t('tokens.mcp.desc') }}</p>

    <div class="mb-4">
      <div class="label-sys mb-1">{{ t('tokens.mcp.endpoint') }}</div>
      <div class="flex items-center gap-2">
        <code class="flex-1 truncate rounded-lg border border-line bg-bg px-3 py-2 font-mono text-xs text-fg">{{ endpoint }}</code>
        <button type="button" class="btn-ghost" :title="t('common.copy')" @click="copy('endpoint', endpoint)">
          <Icon :name="copiedKey === 'endpoint' ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
        </button>
      </div>
    </div>

    <div class="mb-4 rounded-lg border border-line bg-surface-2 p-3">
      <div class="mb-1 flex items-center gap-1.5 text-xs font-medium text-fg">
        <Icon name="lucide:log-in" class="size-3.5 text-accent" />
        {{ t('tokens.mcp.claudeWeb') }}
      </div>
      <p class="text-xs leading-relaxed text-fg-muted">{{ t('tokens.mcp.claudeWebSteps') }}</p>
    </div>

    <ol class="mb-4 list-decimal space-y-1 pl-5 text-xs text-fg-muted">
      <li>{{ t('tokens.mcp.step1') }}</li>
      <li>{{ t('tokens.mcp.step2') }}</li>
      <li>{{ t('tokens.mcp.step3') }}</li>
    </ol>

    <div class="space-y-3">
      <div v-for="s in snippets" :key="s.key">
        <div class="mb-1 flex items-center justify-between">
          <span class="text-xs font-medium text-fg">{{ s.title }}</span>
          <button type="button" class="btn-ghost text-xs" @click="copy(s.key, s.code)">
            <Icon :name="copiedKey === s.key ? 'lucide:check' : 'lucide:copy'" class="mr-1 inline size-3.5" />
            {{ copiedKey === s.key ? t('common.copied') : t('common.copy') }}
          </button>
        </div>
        <pre class="overflow-x-auto rounded-lg border border-line bg-bg p-3 font-mono text-xs text-fg">{{ s.code }}</pre>
      </div>
    </div>

    <p class="mt-4 text-xs text-fg-muted">{{ t('tokens.mcp.note') }}</p>
  </section>
</template>
