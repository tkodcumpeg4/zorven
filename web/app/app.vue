<script setup lang="ts">
// Belge dili panel diline uyar: CSS uppercase Turkce i/I -> İ/I doğru işlensin.
const { locale } = useI18n()
useHead({ htmlAttrs: { lang: computed(() => (['tr', 'ja'].includes(locale.value) ? locale.value : 'en')) } })
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
  <ToastContainer />
</template>
