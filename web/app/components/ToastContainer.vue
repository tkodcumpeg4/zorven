<script setup lang="ts">
const { toasts, remove } = useToast()

function iconName(type: string) {
  switch (type) {
    case 'success':
      return 'lucide:check-circle-2'
    case 'error':
      return 'lucide:alert-circle'
    case 'warn':
      return 'lucide:alert-triangle'
    default:
      return 'lucide:info'
  }
}

function typeClasses(type: string) {
  switch (type) {
    case 'success':
      return 'border-accent/30 bg-surface/95 text-fg shadow-accent/5'
    case 'error':
      return 'border-danger/40 bg-surface/95 text-fg shadow-danger/5'
    case 'warn':
      return 'border-warn/40 bg-surface/95 text-fg shadow-warn/5'
    default:
      return 'border-info/40 bg-surface/95 text-fg shadow-info/5'
  }
}

function iconColor(type: string) {
  switch (type) {
    case 'success':
      return 'text-accent-bright'
    case 'error':
      return 'text-danger'
    case 'warn':
      return 'text-warn'
    default:
      return 'text-info'
  }
}
</script>

<template>
  <div
    class="fixed bottom-5 right-5 z-50 flex w-full max-w-sm flex-col gap-2.5 pointer-events-none px-4 sm:px-0"
    aria-live="polite"
    aria-atomic="true"
  >
    <TransitionGroup
      enter-active-class="transform ease-out duration-200 transition"
      enter-from-class="translate-y-2 opacity-0 sm:translate-y-0 sm:translate-x-4"
      enter-to-class="translate-y-0 opacity-100 sm:translate-x-0"
      leave-active-class="transition ease-in duration-150"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0 scale-95"
    >
      <div
        v-for="t in toasts"
        :key="t.id"
        class="pointer-events-auto flex items-start gap-3 rounded-xl border p-3.5 shadow-2xl backdrop-blur-md"
        :class="typeClasses(t.type)"
      >
        <Icon :name="iconName(t.type)" class="size-5 shrink-0 mt-0.5" :class="iconColor(t.type)" />
        <div class="flex-1 min-w-0">
          <h4 v-if="t.title" class="text-xs font-semibold tracking-wide text-fg">{{ t.title }}</h4>
          <p class="text-xs text-fg leading-relaxed break-words">{{ t.message }}</p>
        </div>
        <button
          type="button"
          class="shrink-0 text-fg-muted hover:text-fg transition-colors cursor-pointer p-0.5 -mr-1 -mt-1 rounded"
          @click="remove(t.id)"
        >
          <Icon name="lucide:x" class="size-4" />
          <span class="sr-only">Kapat</span>
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>
