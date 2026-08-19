<template>
  <div class="card p-4">
    <div class="mb-3 flex min-h-8 flex-wrap items-center justify-between gap-2">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ title }}</h3>
      <div class="flex items-center gap-2">
        <div
          v-if="metric === 'network'"
          class="inline-flex rounded-md border border-gray-200 bg-gray-50 p-0.5 dark:border-dark-600 dark:bg-dark-800"
          role="group"
          :aria-label="title"
        >
          <button
            type="button"
            data-testid="network-mode-rate"
            class="rounded px-2.5 py-1 text-xs font-medium transition-colors"
            :class="networkMode === 'rate' ? activeModeClass : inactiveModeClass"
            @click="networkMode = 'rate'"
          >
            {{ t('admin.dashboard.networkRate') }}
          </button>
          <button
            type="button"
            data-testid="network-mode-daily"
            class="rounded px-2.5 py-1 text-xs font-medium transition-colors"
            :class="networkMode === 'daily' ? activeModeClass : inactiveModeClass"
            @click="networkMode = 'daily'"
          >
            {{ t('admin.dashboard.networkDailyUsage') }}
          </button>
        </div>
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
    </div>

    <div
      v-if="metric === 'network' && networkMode === 'daily' && networkDaily?.length"
      class="mb-3 flex flex-wrap items-baseline gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400"
    >
      <span>
        {{ t('admin.dashboard.rangeTotalTraffic') }}：
        <strong class="text-sm text-gray-900 dark:text-white">{{ formatTrafficBytes(networkTotals?.total_bytes) }}</strong>
      </span>
      <span>{{ t('admin.dashboard.rangeReceiveTraffic') }} {{ formatTrafficBytes(networkTotals?.receive_bytes) }}</span>
      <span>{{ t('admin.dashboard.rangeTransmitTraffic') }} {{ formatTrafficBytes(networkTotals?.transmit_bytes) }}</span>
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
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Line } from 'vue-chartjs'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type {
  DashboardNetworkTrafficDailyPoint,
  DashboardNetworkTrafficTotals,
  DashboardSystemMetricPoint
} from '@/api/admin/dashboard'

const props = defineProps<{
  title: string
  metric: 'cpu' | 'memory' | 'network' | 'disk'
  points: DashboardSystemMetricPoint[]
  networkDaily?: DashboardNetworkTrafficDailyPoint[]
  networkTotals?: DashboardNetworkTrafficTotals
  loading: boolean
}>()
defineEmits<{ refresh: [] }>()
const { t } = useI18n()

const networkMode = ref<'rate' | 'daily'>('rate')
const activeModeClass = 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
const inactiveModeClass = 'text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'
// 实时带宽按行业惯例展示为 Mbps；每日累计流量仍使用字节容量单位。
const BITS_PER_BYTE = 8

const formatTrafficBytes = (raw: number | null | undefined): string => {
  const value = Number(raw ?? 0)
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const unitIndex = Math.min(Math.floor(Math.log(value) / Math.log(1000)), units.length - 1)
  const scaled = value / 1000 ** unitIndex
  return `${scaled.toLocaleString('zh-CN', { minimumFractionDigits: unitIndex >= 3 ? 2 : 0, maximumFractionDigits: 2 })} ${units[unitIndex]}`
}

const formatRate = (value: number): string => {
  const digits = value > 0 && value < 0.01 ? 4 : 2
  return `${value.toLocaleString('zh-CN', { minimumFractionDigits: digits, maximumFractionDigits: digits })} Mbps`
}

const ratePoints = computed(() => props.points.filter(
  (point) => point.network_receive_bytes_per_second != null || point.network_transmit_bytes_per_second != null
))

const resourcePoints = computed(() => props.points.filter((point) => {
  if (props.metric === 'cpu') return point.cpu_usage_percent != null
  if (props.metric === 'memory') return point.memory_usage_percent != null
  return point.disk_usage_percent != null
}))

const chartData = computed(() => {
  if (props.metric === 'network' && networkMode.value === 'daily') {
    const points = props.networkDaily ?? []
    if (!points.length) return null
    return {
      labels: points.map((point) => point.bucket_date),
      datasets: [
        {
          label: t('admin.dashboard.networkReceive'),
          data: points.map((point) => point.receive_bytes / 1_000_000_000),
          borderColor: '#0284c7',
          backgroundColor: 'rgba(2,132,199,.08)',
          fill: true,
          tension: 0.25
        },
        {
          label: t('admin.dashboard.networkTransmit'),
          data: points.map((point) => point.transmit_bytes / 1_000_000_000),
          borderColor: '#ea580c',
          backgroundColor: 'rgba(234,88,12,.08)',
          fill: true,
          tension: 0.25
        }
      ]
    }
  }

  if (props.metric === 'network') {
    if (!ratePoints.value.length) return null
    return {
      labels: ratePoints.value.map((point) => new Date(point.time).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })),
      datasets: [
        {
          label: t('admin.dashboard.networkReceive'),
          data: ratePoints.value.map((point) => ((point.network_receive_bytes_per_second ?? 0) * BITS_PER_BYTE) / 1_000_000),
          borderColor: '#0284c7',
          backgroundColor: 'rgba(2,132,199,.08)',
          tension: 0.25
        },
        {
          label: t('admin.dashboard.networkTransmit'),
          data: ratePoints.value.map((point) => ((point.network_transmit_bytes_per_second ?? 0) * BITS_PER_BYTE) / 1_000_000),
          borderColor: '#ea580c',
          backgroundColor: 'rgba(234,88,12,.08)',
          tension: 0.25
        }
      ]
    }
  }

  if (!resourcePoints.value.length) return null
  const key = props.metric === 'cpu' ? 'cpu_usage_percent' : props.metric === 'memory' ? 'memory_usage_percent' : 'disk_usage_percent'
  const color = props.metric === 'cpu' ? '#dc2626' : props.metric === 'memory' ? '#7c3aed' : '#059669'
  return {
    labels: resourcePoints.value.map((point) => new Date(point.time).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })),
    datasets: [{ label: '%', data: resourcePoints.value.map((point) => point[key] ?? 0), borderColor: color, backgroundColor: `${color}14`, fill: true, tension: 0.25 }]
  }
})

const yAxisUnit = computed(() => {
  if (props.metric !== 'network') return '%'
  return networkMode.value === 'daily' ? 'GB' : 'Mbps'
})

const options = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' as const },
  plugins: {
    legend: { display: props.metric === 'network' },
    tooltip: {
      callbacks: {
        label: (context: { dataset: { label?: string }; raw: unknown }) => {
          const label = context.dataset.label ? `${context.dataset.label}: ` : ''
          const value = Number(context.raw ?? 0)
          if (props.metric !== 'network') return `${label}${value.toFixed(1)}%`
          if (networkMode.value === 'daily') return `${label}${value.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} GB`
          return `${label}${formatRate(value)}`
        }
      }
    }
  },
  scales: {
    x: { ticks: { maxTicksLimit: 6 } },
    y: {
      beginAtZero: true,
      title: { display: true, text: yAxisUnit.value },
      ticks: {
        callback: (raw: string | number) => {
          const value = Number(raw)
          if (props.metric !== 'network') return `${value}%`
          return value.toLocaleString('zh-CN', { maximumFractionDigits: value > 0 && value < 0.01 ? 4 : 2 })
        }
      }
    }
  }
}))
</script>
