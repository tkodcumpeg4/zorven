<script setup lang="ts">
/**
 * Referans sitedeki (finagent.tkod.tr) yıldız/parçacık arka planı.
 * Sabit konumlu, içeriğin arkasında, tıklamayı engellemez.
 * - prefers-reduced-motion: hareket durur, statik alan çizilir
 * - sekme gizliyken RAF durdurulur (boşuna CPU yakmasın)
 */
const canvas = ref<HTMLCanvasElement | null>(null)

onMounted(() => {
  const el = canvas.value
  if (!el) return
  const ctx = el.getContext('2d')
  if (!ctx) return

  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  let raf = 0
  let stars: { x: number, y: number, r: number, a: number, vy: number }[] = []

  function seed() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    el!.width = window.innerWidth * dpr
    el!.height = window.innerHeight * dpr
    ctx!.setTransform(dpr, 0, 0, dpr, 0, 0)

    // Yoğunluk alana göre; çok büyük ekranlarda üst sınır var
    const count = Math.min(Math.round((window.innerWidth * window.innerHeight) / 9000), 190)
    stars = Array.from({ length: count }, () => ({
      x: Math.random() * window.innerWidth,
      y: Math.random() * window.innerHeight,
      r: Math.random() * 1.1 + 0.3,
      a: Math.random() * 0.5 + 0.15,
      vy: Math.random() * 0.05 + 0.015,
    }))
  }

  function draw() {
    ctx!.clearRect(0, 0, window.innerWidth, window.innerHeight)
    for (const s of stars) {
      ctx!.beginPath()
      ctx!.arc(s.x, s.y, s.r, 0, Math.PI * 2)
      ctx!.fillStyle = 'rgba(245, 245, 245, ' + s.a + ')'
      ctx!.fill()
    }
  }

  function tick() {
    for (const s of stars) {
      s.y -= s.vy
      if (s.y < -2) {
        s.y = window.innerHeight + 2
        s.x = Math.random() * window.innerWidth
      }
    }
    draw()
    raf = requestAnimationFrame(tick)
  }

  function start() {
    if (reduced) { draw(); return }
    cancelAnimationFrame(raf)
    raf = requestAnimationFrame(tick)
  }

  function onVisibility() {
    if (document.hidden) cancelAnimationFrame(raf)
    else start()
  }

  seed()
  start()

  window.addEventListener('resize', () => { seed(); draw() })
  document.addEventListener('visibilitychange', onVisibility)

  onUnmounted(() => {
    cancelAnimationFrame(raf)
    document.removeEventListener('visibilitychange', onVisibility)
  })
})
</script>

<template>
  <canvas
    ref="canvas"
    class="pointer-events-none fixed inset-0 z-0 h-screen w-screen transition-opacity duration-300 dark:opacity-100 opacity-20"
    aria-hidden="true"
  />
</template>
