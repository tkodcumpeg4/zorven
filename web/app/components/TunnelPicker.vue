<script setup lang="ts">
// İki aşamalı tünel seçici: önce istemci, sonra o istemcideki tüneller.
// modelValue = tünel kimliği ('' = boşta / seçilmedi). İstemci değişince
// tünel seçimi sıfırlanır ama boşa düşürme yalnızca açıkça "boşta" seçilince yapılır.
import type { Client, Tunnel } from '~/types/api'

const props = withDefaults(defineProps<{
  modelValue: string
  tunnels: Tunnel[]
  clients: Client[]
  /** Boş seçenek etiketi (ör. "— Boşta —" ya da "Tünel seç…"). */
  emptyLabel: string
  /** true: istemci listesinde "boşta" seçeneği vardır ve seçilince '' yayılır. */
  allowIdle?: boolean
  size?: 'sm' | 'md'
}>(), { allowIdle: false, size: 'sm' })

const emit = defineEmits<{ 'update:modelValue': [string] }>()
const { t } = useI18n()

const clientName = (id: string) => props.clients.find(c => c.id === id)?.name || t('domains.unknownClient')

// Yalnızca tüneli olan istemciler; ad sırasına göre.
const groups = computed(() => {
  const m = new Map<string, Tunnel[]>()
  for (const tn of props.tunnels) {
    const k = tn.client_id || ''
    if (!m.has(k)) m.set(k, [])
    m.get(k)!.push(tn)
  }
  return [...m.entries()]
    .map(([id, list]) => ({ id, name: clientName(id), online: props.clients.find(c => c.id === id)?.status === 'online', list }))
    .sort((a, b) => a.name.localeCompare(b.name))
})

const ownerOf = (tid: string) => props.tunnels.find(x => x.id === tid)?.client_id ?? ''
const client = ref(props.modelValue ? ownerOf(props.modelValue) : '')
watch(() => [props.modelValue, props.tunnels.length], () => {
  if (props.modelValue) client.value = ownerOf(props.modelValue)
})

const clientTunnels = computed(() => groups.value.find(g => g.id === client.value)?.list ?? [])
const picked = computed(() => (props.modelValue && ownerOf(props.modelValue) === client.value ? props.modelValue : ''))

function onClient(v: string) {
  client.value = v
  if (v === '__idle') {
    client.value = ''
    if (props.modelValue) emit('update:modelValue', '')
    return
  }
  // Tek tünel varsa doğrudan onu seç: gereksiz ikinci tıklama olmasın.
  const list = clientTunnels.value
  if (list.length === 1 && list[0]) emit('update:modelValue', list[0].id)
  else if (!props.allowIdle && props.modelValue && ownerOf(props.modelValue) !== v) emit('update:modelValue', '')
}

const cls = computed(() => props.size === 'md'
  ? 'cursor-pointer rounded border border-line bg-bg px-2 py-1.5 text-xs focus:border-accent'
  : 'cursor-pointer rounded border border-line bg-surface px-2 py-1 text-xs font-mono text-fg transition-colors hover:border-accent focus:border-accent')
</script>

<template>
  <div class="flex flex-wrap items-center gap-1.5">
    <select :class="[cls, 'min-w-[140px] max-w-[200px]']" :value="client" :aria-label="t('domains.selectClient')" @change="onClient(($event.target as HTMLSelectElement).value)">
      <option value="" disabled>{{ t('domains.selectClient') }}</option>
      <option v-if="allowIdle" value="__idle">{{ emptyLabel }}</option>
      <option v-for="g in groups" :key="g.id" :value="g.id">{{ g.online ? '● ' : '○ ' }}{{ g.name }} ({{ g.list.length }})</option>
    </select>
    <select
      :class="[cls, 'min-w-[180px] flex-1']"
      :value="picked"
      :disabled="!client"
      :aria-label="emptyLabel"
      @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)"
    >
      <option value="" :disabled="!allowIdle">{{ client && !clientTunnels.length ? t('domains.noTunnelsForClient') : emptyLabel }}</option>
      <option v-for="tn in clientTunnels" :key="tn.id" :value="tn.id">{{ tn.target }} ({{ tn.id }})</option>
    </select>
  </div>
</template>
