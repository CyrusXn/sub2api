<template>
  <div class="flex max-w-full flex-wrap items-center justify-center gap-x-2 gap-y-1 text-xs text-gray-600 dark:text-gray-300 sm:gap-x-4 xl:text-sm">
    <span class="font-semibold text-gray-800 dark:text-gray-100">
      {{ t('admin.dashboard.realtimePerformance') }}
    </span>
    <span class="whitespace-nowrap">RPM <strong class="text-red-700 dark:text-red-300">{{ formatMetric(metricsStore.metrics?.requests_per_minute) }}</strong></span>
    <span class="whitespace-nowrap">TPM <strong class="text-red-700 dark:text-red-300">{{ formatMetric(metricsStore.metrics?.tokens_per_minute) }}</strong></span>
    <span class="whitespace-nowrap">
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
    <Select
      class="w-40"
      :model-value="autoRefreshEnabled ? autoRefreshIntervalSeconds : 0"
      :options="autoRefreshOptions"
      :searchable="true"
      :aria-label="`${t('admin.dashboard.realtimePerformance')} · ${t('common.autoRefresh.title')}`"
      @update:model-value="setAutoRefreshInterval"
    >
      <template #selected>
        <span class="inline-flex items-center gap-1.5 text-xs">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': autoRefreshEnabled }" />
          {{ autoRefreshEnabled
            ? t('common.autoRefresh.countdown', { seconds: autoRefreshCountdown })
            : t('common.autoRefresh.title') }}
        </span>
      </template>
    </Select>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { useAdminRealtimeMetricsStore } from '@/stores/adminRealtimeMetrics'

const emit = defineEmits<{
  'refresh-dashboard-metrics': []
}>()

const route = useRoute()
const { t } = useI18n()
const metricsStore = useAdminRealtimeMetricsStore()

// 顶部指标独立刷新，避免与账号列表周期耦合或反复加载完整仪表盘。
const autoRefresh = useAutoRefresh({
  storageKey: 'admin-realtime-auto-refresh',
  defaultInterval: 10,
  onRefresh: () => metricsStore.refresh(),
  shouldPause: () => metricsStore.loading,
})
const {
  enabled: autoRefreshEnabled,
  intervalSeconds: autoRefreshIntervalSeconds,
  countdown: autoRefreshCountdown,
} = autoRefresh
const autoRefreshOptions = computed(() => [
  { value: 0, label: t('common.disabled') },
  ...autoRefresh.intervals.map(seconds => ({
    value: seconds,
    label: t('common.autoRefresh.seconds', { n: seconds }),
  })),
])

const setAutoRefreshInterval = (seconds: number) => {
  if (seconds === 0) {
    autoRefresh.setEnabled(false)
    return
  }
  if (!autoRefresh.intervals.includes(seconds)) return
  autoRefresh.setInterval(seconds)
  autoRefresh.setEnabled(true)
}

onMounted(() => {
  if (autoRefreshEnabled.value) autoRefresh.start()
})

const formatMetric = (value: number | null | undefined): string => {
  if (value === null || value === undefined || !Number.isFinite(Number(value))) return '--'
  const safeValue = Number(value)
  if (Math.abs(safeValue) >= 1_000_000_000) return `${(safeValue / 1_000_000_000).toFixed(2)}B`
  if (Math.abs(safeValue) >= 1_000_000) return `${(safeValue / 1_000_000).toFixed(2)}M`
  if (Math.abs(safeValue) >= 1_000) return `${(safeValue / 1_000).toFixed(2)}K`
  return safeValue.toLocaleString()
}

// 即使关闭自动刷新，管理员切换页面时仍读取一次最新值。
watch(() => route.fullPath, () => {
  void metricsStore.refresh()
}, { immediate: true })

const handleRefresh = async () => {
  await metricsStore.refresh()
  if (autoRefreshEnabled.value) autoRefresh.resetCountdown()
  if (route.path === '/admin/dashboard') {
    emit('refresh-dashboard-metrics')
  }
}
</script>
