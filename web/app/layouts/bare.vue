<script setup lang="ts">
// Sidebar/ust bar olmayan yerlesim (ayri pencere). Tema, dil ve oturum/yonetici
// anahtari kapisi (AdminKeyGate) varsayilan yerlesimle aynidir.
const { initTheme } = useTheme()
onMounted(() => { initTheme() })
</script>

<template>
  <div class="relative min-h-screen">
    <StarField />
    <main class="relative z-10 mx-auto max-w-[1600px] px-4 py-4 sm:px-5">
      <AdminKeyGate>
        <slot />
      </AdminKeyGate>
    </main>
  </div>
</template>
