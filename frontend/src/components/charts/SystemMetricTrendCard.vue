<template>
  <div class="card p-4">
    <div class="mb-3 flex items-center justify-between">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ title }}</h3>
      <button
        type="button"
        class="rounded p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
        :title="t('common.refresh')"
        :disabled="loading"
        @click="$emit('refresh')"
      >
        <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
      </button>
    </div>
    <div class="h-56">
      <div v-if="loading" class="flex h-full items-center justify-center"><LoadingSpinner size="sm" /></div>
      <Line v-else-if="chartData" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.dashboard.noHistoricalMetrics') }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Line } from 'vue-chartjs'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { DashboardSystemMetricPoint } from '@/api/admin/dashboard'

const props = defineProps<{
  title: string
  metric: 'cpu' | 'memory' | 'network' | 'disk'
  points: DashboardSystemMetricPoint[]
  loading: boolean
}>()
defineEmits<{ refresh: [] }>()
const { t } = useI18n()

const chartData = computed(() => {
  const points = props.points.filter((point) => {
    if (props.metric === 'cpu') return point.cpu_usage_percent != null
    if (props.metric === 'memory') return point.memory_usage_percent != null
    if (props.metric === 'disk') return point.disk_usage_percent != null
    return point.network_receive_bytes_per_second != null || point.network_transmit_bytes_per_second != null
  })
  if (!points.length) return null
  const labels = points.map((point) => new Date(point.time).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }))
  if (props.metric === 'network') {
    return { labels, datasets: [
      { label: t('admin.dashboard.networkReceive'), data: points.map((p) => (p.network_receive_bytes_per_second ?? 0) / 1024 / 1024), borderColor: '#0284c7', backgroundColor: 'rgba(2,132,199,.08)', tension: .25 },
      { label: t('admin.dashboard.networkTransmit'), data: points.map((p) => (p.network_transmit_bytes_per_second ?? 0) / 1024 / 1024), borderColor: '#ea580c', backgroundColor: 'rgba(234,88,12,.08)', tension: .25 }
    ] }
  }
  const key = props.metric === 'cpu' ? 'cpu_usage_percent' : props.metric === 'memory' ? 'memory_usage_percent' : 'disk_usage_percent'
  const color = props.metric === 'cpu' ? '#dc2626' : props.metric === 'memory' ? '#7c3aed' : '#059669'
  return { labels, datasets: [{ label: '%', data: points.map((point) => point[key] ?? 0), borderColor: color, backgroundColor: `${color}14`, fill: true, tension: .25 }] }
})

const options = { responsive: true, maintainAspectRatio: false, interaction: { intersect: false, mode: 'index' as const }, plugins: { legend: { display: props.metric === 'network' } }, scales: { x: { ticks: { maxTicksLimit: 6 } }, y: { beginAtZero: true } } }
</script>
