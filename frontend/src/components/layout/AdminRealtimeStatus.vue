<template>
  <div class="flex items-center gap-5 whitespace-nowrap text-sm text-gray-600 dark:text-gray-300">
    <span class="font-semibold text-gray-800 dark:text-gray-100">
      {{ t('admin.dashboard.realtimePerformance') }}
    </span>
    <span>RPM <strong class="text-gray-900 dark:text-white">{{ formatMetric(metricsStore.metrics?.requests_per_minute) }}</strong></span>
    <span>TPM <strong class="text-gray-900 dark:text-white">{{ formatMetric(metricsStore.metrics?.tokens_per_minute) }}</strong></span>
    <span>
      {{ t('admin.dashboard.currentConcurrency') }}
      <strong class="text-red-600 dark:text-red-400">{{ formatMetric(metricsStore.metrics?.active_requests) }}</strong>
    </span>
    <button
      type="button"
      data-testid="admin-realtime-refresh"
      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:cursor-wait disabled:opacity-60 dark:hover:bg-dark-800 dark:hover:text-white"
      :title="t('common.refresh')"
      :disabled="metricsStore.loading"
      @click="handleRefresh"
    >
      <Icon name="refresh" size="sm" :class="{ 'animate-spin': metricsStore.loading }" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import Icon from '@/components/icons/Icon.vue'
import { useAdminRealtimeMetricsStore } from '@/stores/adminRealtimeMetrics'

const emit = defineEmits<{
  'refresh-dashboard-metrics': []
}>()

const route = useRoute()
const { t } = useI18n()
const metricsStore = useAdminRealtimeMetricsStore()

const formatMetric = (value: number | null | undefined): string => {
  if (value === null || value === undefined || !Number.isFinite(Number(value))) return '--'
  const safeValue = Number(value)
  if (Math.abs(safeValue) >= 1_000_000_000) return `${(safeValue / 1_000_000_000).toFixed(2)}B`
  if (Math.abs(safeValue) >= 1_000_000) return `${(safeValue / 1_000_000).toFixed(2)}M`
  if (Math.abs(safeValue) >= 1_000) return `${(safeValue / 1_000).toFixed(2)}K`
  return safeValue.toLocaleString()
}

// 管理员每次切换页面读取一次最新值，不启动后台轮询。
watch(() => route.fullPath, () => {
  void metricsStore.refresh()
}, { immediate: true })

const handleRefresh = async () => {
  await metricsStore.refresh()
  if (route.path === '/admin/dashboard') {
    emit('refresh-dashboard-metrics')
  }
}
</script>
