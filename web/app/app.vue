<script setup lang="ts">
// Belge dili panel diline uyar: CSS uppercase Turkce i/I -> İ/I doğru işlensin.
const { locale } = useI18n()
useHead({ htmlAttrs: { lang: computed(() => (locale.value === 'tr' ? 'tr' : 'en')) } })
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
  <ToastContainer />
</template>
