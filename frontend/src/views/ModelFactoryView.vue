<template>
  <AppLayout v-if="authStore.isAuthenticated">
    <ModelPlazaContent
      :response="data"
      :loading="loading"
      :error="loadFailed"
      embedded
      title-key="modelFactory.title"
      description-key="modelFactory.description"
    />
  </AppLayout>
  <div v-else class="min-h-screen bg-gray-50 dark:bg-dark-950">
    <PlazaNavBar />
    <main class="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <ModelPlazaContent
        :response="data"
        :loading="loading"
        :error="loadFailed"
        hide-anonymous-hint
        title-key="modelFactory.title"
        description-key="modelFactory.description"
      />
    </main>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import PlazaNavBar from '@/components/modelPlaza/PlazaNavBar.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import ModelPlazaContent from '@/components/modelPlaza/ModelPlazaContent.vue'
import { getModelFactory } from '@/api/modelFactory'
import type { ModelPlazaResponse } from '@/api/modelPlaza'

const data = ref<ModelPlazaResponse | null>(null)
const authStore = useAuthStore()
const appStore = useAppStore()
const loading = ref(true)
const loadFailed = ref(false)

async function loadModelFactory() {
  loading.value = true
  loadFailed.value = false
  try {
    data.value = await getModelFactory()
  } catch {
    loadFailed.value = true
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void appStore.fetchPublicSettings()
  void loadModelFactory()
  window.addEventListener('model-factory-refresh', loadModelFactory)
})
onUnmounted(() => window.removeEventListener('model-factory-refresh', loadModelFactory))
</script>
