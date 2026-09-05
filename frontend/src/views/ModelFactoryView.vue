<template>
  <AppLayout>
    <ModelPlazaContent
      :response="data"
      :loading="loading"
      :error="loadFailed"
      embedded
      title-key="modelFactory.title"
      description-key="modelFactory.description"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import ModelPlazaContent from '@/components/modelPlaza/ModelPlazaContent.vue'
import { getModelFactory } from '@/api/modelFactory'
import type { ModelPlazaResponse } from '@/api/modelPlaza'

const data = ref<ModelPlazaResponse | null>(null)
const loading = ref(true)
const loadFailed = ref(false)

async function loadModelFactory() {
  // 模型工厂每次进入页面都重新拉取，确保展示当前分组账号的最新模型集合。
  try {
    const result = await getModelFactory()
    // 复用模型广场成熟的筛选与展示表格，模型工厂只提供账号模型名称和分组倍率。
    data.value = {
      description: '',
      groups: result.groups.map((group) => ({
        id: group.id,
        name: group.name,
        description: '',
        platform: group.platform,
        subscription_type: 'standard',
        rate_multiplier: group.rate_multiplier,
        peak_rate_enabled: false,
        peak_start: '',
        peak_end: '',
        peak_rate_multiplier: 1,
        is_exclusive: false,
        image_rate_independent: false,
        image_rate_multiplier: 1,
        long_context_pricing_enabled: false,
        models: group.models.map((name) => ({ name, platform: group.platform, pricing: null, official_pricing: null }))
      }))
    }
  } catch {
    loadFailed.value = true
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void loadModelFactory()
  window.addEventListener('model-factory-refresh', loadModelFactory)
})
onUnmounted(() => window.removeEventListener('model-factory-refresh', loadModelFactory))
</script>
