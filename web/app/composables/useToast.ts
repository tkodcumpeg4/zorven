export interface ToastMessage {
  id: string
  type: 'success' | 'error' | 'warn' | 'info'
  title?: string
  message: string
  duration?: number
  timer?: any
}

const toasts = ref<ToastMessage[]>([])

export function useToast() {
  function remove(id: string) {
    const idx = toasts.value.findIndex(t => t.id === id)
    if (idx !== -1) {
      const item = toasts.value[idx]
      if (item && item.timer) {
        clearTimeout(item.timer)
      }
      toasts.value.splice(idx, 1)
    }
  }

  function add(type: ToastMessage['type'], message: string, title?: string, duration = 4000) {
    const id = 'toast_' + Math.random().toString(36).substring(2, 9)
    let timer: any = null
    if (duration > 0) {
      timer = setTimeout(() => {
        remove(id)
      }, duration)
    }
    const item: ToastMessage = { id, type, title, message, duration, timer }
    toasts.value.push(item)
    return id
  }

  function success(message: string, title?: string, duration = 4000) {
    return add('success', message, title, duration)
  }

  function error(message: string, title?: string, duration = 5000) {
    return add('error', message, title, duration)
  }

  function warn(message: string, title?: string, duration = 4500) {
    return add('warn', message, title, duration)
  }

  function info(message: string, title?: string, duration = 4000) {
    return add('info', message, title, duration)
  }

  function clear() {
    toasts.value.forEach(t => {
      if (t.timer) clearTimeout(t.timer)
    })
    toasts.value = []
  }

  return {
    toasts: readonly(toasts),
    add,
    remove,
    clear,
    success,
    error,
    warn,
    info,
  }
}
