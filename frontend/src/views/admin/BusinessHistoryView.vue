<template>
  <AppLayout>
    <div data-test="business-history-content" class="space-y-6">
      <section class="flex flex-col gap-3 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end" aria-label="经营历史查询">
        <label class="w-full sm:w-48">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.startDate') }}</span>
          <input v-model="startDate" data-test="history-start-date" type="date" class="input h-10 w-full" />
        </label>
        <label class="w-full sm:w-48">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.endDate') }}</span>
          <input v-model="endDate" data-test="history-end-date" type="date" class="input h-10 w-full" />
        </label>
        <button data-test="history-query" type="button" class="btn btn-primary h-10" :disabled="loading" @click="loadSummary">
          <Icon name="search" size="sm" />
          {{ t('admin.businessHistory.query') }}
        </button>
        <button type="button" class="btn btn-secondary h-10 w-10 p-0" :title="t('common.refresh')" :disabled="loading" @click="loadSummary">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
      </section>

      <p v-if="loadError" class="rounded border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">
        {{ t('admin.businessHistory.loadFailed') }}
      </p>

      <section class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4" aria-label="经营指标">
        <article class="card p-5">
          <div class="flex items-center gap-3">
            <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30"><Icon name="document" size="md" class="text-blue-600 dark:text-blue-400" /></div>
            <div>
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalRequests') }}</p>
              <p data-test="range-requests" class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ formatNumber(summary?.range.total_requests) }}</p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30"><Icon name="cube" size="md" class="text-amber-600 dark:text-amber-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalTokens') }}</p>
              <p data-test="range-tokens" class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ formatTokens(summary?.range.total_tokens) }}</p>
              <p data-test="token-breakdown" class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
                {{ t('admin.businessHistory.inputTokens') }} {{ formatTokens(summary?.range.input_tokens) }} ·
                {{ t('admin.businessHistory.outputTokens') }} {{ formatTokens(summary?.range.output_tokens) }} ·
                {{ t('admin.businessHistory.cacheCreationTokens') }} {{ formatTokens(summary?.range.cache_creation_tokens) }} ·
                {{ t('admin.businessHistory.cacheReadTokens') }} {{ formatTokens(summary?.range.cache_read_tokens) }}
              </p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-emerald-100 p-2 dark:bg-emerald-900/30"><Icon name="dollar" size="md" class="text-emerald-600 dark:text-emerald-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalConsumption') }}</p>
              <p data-test="range-consumption" class="mt-1 flex flex-wrap items-baseline gap-2 text-2xl font-bold text-gray-900 dark:text-white">
                <span>{{ formatMoney(summary?.range.actual_cost) }}</span>
                <span class="text-gray-300 dark:text-dark-500">/</span>
                <span class="text-emerald-600 dark:text-emerald-400">{{ formatMoney(summary?.range.actual_cost_excluding_admin) }}</span>
              </p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.allAccounts') }} / {{ t('admin.businessHistory.excludingAdmin') }}</p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="grid grid-cols-2 gap-4">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <Icon name="creditCard" size="sm" class="shrink-0 text-emerald-600 dark:text-emerald-400" />
                <p class="truncate text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.userTotalRecharge') }}</p>
              </div>
              <p data-test="user-total-recharge" class="mt-2 text-xl font-bold text-emerald-600 dark:text-emerald-400">{{ formatMoney(summary?.range.recharge_amount) }}</p>
            </div>
            <div class="min-w-0 border-l border-gray-200 pl-4 dark:border-dark-700">
              <div class="flex items-center gap-2">
                <Icon name="dollar" size="sm" class="shrink-0 text-teal-600 dark:text-teal-400" />
                <p class="truncate text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.userBalanceTotal') }}</p>
              </div>
              <p data-test="user-balance-total" class="mt-2 text-xl font-bold text-teal-600 dark:text-teal-400">{{ formatMoney(summary?.range_user_balance_total) }}</p>
              <p data-test="user-balance-snapshot-hint" class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">{{ balanceSnapshotHint }}</p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-red-100 p-2 dark:bg-red-900/30"><Icon name="dollar" size="md" class="text-red-600 dark:text-red-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamRechargeTotal') }}</p>
              <p data-test="upstream-recharge-total" class="mt-1 text-2xl font-bold text-red-600 dark:text-red-400">
                {{ formatMoney(summary?.range_upstream_recharge_total) }}
              </p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamRechargeDescription') }}</p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-sky-100 p-2 dark:bg-sky-900/30"><Icon name="database" size="md" class="text-sky-600 dark:text-sky-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamBalanceTotal') }}</p>
              <p data-test="upstream-balance-total" class="mt-1 text-2xl font-bold text-sky-600 dark:text-sky-400">{{ formatMoney(summary?.range_upstream_balance_total) }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamBalanceDescription') }}</p>
              <p data-test="upstream-balance-snapshot-hint" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ balanceSnapshotHint }}</p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-orange-100 p-2 dark:bg-orange-900/30"><Icon name="chart" size="md" class="text-orange-600 dark:text-orange-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamTotalConsumption') }}</p>
              <p data-test="upstream-total-consumption" class="mt-1 text-2xl font-bold text-orange-600 dark:text-orange-400">{{ formatMoney(upstreamTotalConsumption) }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamTotalConsumptionFormula') }}</p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-violet-100 p-2 dark:bg-violet-900/30"><Icon name="bolt" size="md" class="text-violet-600 dark:text-violet-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalProfit') }}</p>
              <p data-test="total-profit" class="mt-1 flex flex-wrap items-baseline gap-2 text-2xl font-bold text-gray-900 dark:text-white">
                <span>{{ formatMoney(totalProfitAllAccounts) }}</span>
                <span class="text-gray-300 dark:text-dark-500">/</span>
                <span class="text-violet-600 dark:text-violet-400">{{ formatMoney(totalProfitUsers) }}</span>
              </p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalProfitFormula') }}</p>
            </div>
          </div>
        </article>
      </section>

      <section class="card p-4">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.businessHistory.dailyTrend') }}</h2>
          <div class="inline-flex rounded border border-gray-300 p-0.5 dark:border-dark-600" role="group" :aria-label="t('admin.businessHistory.dailyTrend')">
            <button v-for="metric in trendMetrics" :key="metric" type="button" class="rounded px-3 py-1.5 text-xs font-medium" :class="trendMetric === metric ? selectedMetricClass : normalMetricClass" @click="trendMetric = metric">
              {{ t(`admin.businessHistory.${metric}Trend`) }}
            </button>
          </div>
        </div>
        <div class="h-64">
          <div v-if="loading" class="flex h-full items-center justify-center"><LoadingSpinner size="sm" /></div>
          <Line v-else-if="chartData" :data="chartData" :options="chartOptions" />
          <div v-else class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.noData') }}</div>
        </div>
      </section>

      <section class="space-y-3">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.businessHistory.dailyDetails') }}</h2>
        <div class="overflow-x-auto border-y border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[1350px] text-sm">
            <thead class="bg-gray-50 text-left text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2">{{ t('admin.businessHistory.date') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.totalRequests') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.totalTokens') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.totalConsumption') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.excludingAdmin') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.upstreamConsumption') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.upstreamExcludingAdmin') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.profitLegendAll') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.profitLegendExcludingAdmin') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.effectiveRecharge') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="point in daily" :key="point.bucket_date" data-test="history-daily-row" class="text-gray-700 dark:text-gray-200">
                <td class="whitespace-nowrap px-3 py-3 font-medium">{{ formatDate(point.bucket_date) }}</td>
                <td class="px-3 py-3 text-right">{{ formatNumber(point.total_requests) }}</td>
                <td class="px-3 py-3 text-right">{{ formatTokens(point.total_tokens) }}</td>
                <td class="px-3 py-3 text-right">{{ formatMoney(point.actual_cost) }}</td>
                <td class="px-3 py-3 text-right text-emerald-600 dark:text-emerald-400">{{ formatMoney(point.actual_cost_excluding_admin) }}</td>
                <td class="px-3 py-3 text-right text-red-600 dark:text-red-400">{{ formatMoney(point.upstream_cost) }}</td>
                <td class="px-3 py-3 text-right text-red-600 dark:text-red-400">{{ formatMoney(point.upstream_cost_excluding_admin) }}</td>
                <td class="px-3 py-3 text-right font-medium" :class="profitClass(dailyProfit(point, false))" data-test="history-daily-profit-all">{{ formatMoney(dailyProfit(point, false)) }}</td>
                <td class="px-3 py-3 text-right font-medium" :class="profitClass(dailyProfit(point, true))" data-test="history-daily-profit-users">{{ formatMoney(dailyProfit(point, true)) }}</td>
                <td class="px-3 py-3 text-right">{{ formatMoney(point.recharge_amount) }}</td>
              </tr>
              <tr v-if="!loading && daily.length === 0"><td colspan="10" class="px-3 py-10 text-center text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CategoryScale,
  Chart as ChartJS,
  Filler,
  Legend,
  LinearScale,
  LineElement,
  PointElement,
  Tooltip
} from 'chart.js'
import { Line } from 'vue-chartjs'
import { adminAPI } from '@/api/admin'
import type { DashboardBusinessDailyPoint, DashboardBusinessSummary } from '@/api/admin/dashboard'
import Icon from '@/components/icons/Icon.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { useAppStore } from '@/stores/app'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, Filler)

const { t } = useI18n()
const app = useAppStore()
type TrendMetric = 'requests' | 'tokens' | 'consumption' | 'profit'

const today = new Date()
// 经营历史默认从 2026 年 7 月 9 日开始，结束日期仍使用当前日期。
const rangeStart = new Date(2026, 6, 9)

const startDate = ref(formatLocalDate(rangeStart))
const endDate = ref(formatLocalDate(today))
const summary = ref<DashboardBusinessSummary | null>(null)
const loading = ref(false)
const loadError = ref(false)
const trendMetric = ref<TrendMetric>('requests')
const trendMetrics: TrendMetric[] = ['requests', 'tokens', 'consumption', 'profit']
const selectedMetricClass = 'bg-gray-900 text-white dark:bg-gray-100 dark:text-gray-900'
const normalMetricClass = 'text-gray-500 hover:bg-gray-50 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-gray-200'

const daily = computed(() => [...(summary.value?.daily ?? [])].sort((a, b) => a.bucket_date.localeCompare(b.bucket_date)))
// 上游总消费改用区间内上游计价成本：余额是即时值，无法还原任意区间的期初余额，
// 因此原来的“上游总充值 - 上游总余额”无法按日期查询，也和每日收益趋势对不上。
const upstreamTotalConsumption = computed(() => summary.value?.range.upstream_cost ?? null)
// 总收益展示“全部账号 / 排除 admin”两个口径，与每日收益趋势的两条线口径一致。
const totalProfitAllAccounts = computed(() => {
  if (!summary.value) return null
  return summary.value.range.actual_cost - summary.value.range.upstream_cost
})
const totalProfitUsers = computed(() => {
  if (!summary.value) return null
  return summary.value.range.actual_cost_excluding_admin - summary.value.range.upstream_cost_excluding_admin
})
// 余额快照按日采集，所选区间可能一天都没有采集到，此时提示“暂无快照”而不是展示误导性的 $0.00。
const balanceSnapshotHint = computed(() => {
  if (!summary.value) return ''
  const snapshotDate = summary.value.range_balance_snapshot_date
  if (!snapshotDate) return t('admin.businessHistory.balanceSnapshotEmpty')
  return t('admin.businessHistory.balanceSnapshotAsOf', { date: formatDate(snapshotDate) })
})
// 消费与收益都用美元刻度，请求数和 Token 各自单独格式化。
const isMoneyTrend = computed(() => trendMetric.value === 'consumption' || trendMetric.value === 'profit')

const METRIC_COLORS: Record<TrendMetric, string> = {
  requests: '#2563eb',
  tokens: '#d97706',
  consumption: '#059669',
  profit: '#7c3aed'
}

// 每日收益 = 当日消费 - 当日上游计价成本，可能为负数，所以趋势图不强制从 0 起。
// 抽成单一函数：每日明细表格、趋势主线和排除 admin 副线共用同一口径，避免三处算法漂移。
function dailyProfit(point: DashboardBusinessDailyPoint, excludingAdmin: boolean): number {
  return excludingAdmin
    ? point.actual_cost_excluding_admin - point.upstream_cost_excluding_admin
    : point.actual_cost - point.upstream_cost
}

// 亏损日标红、盈利日标绿，和上方汇总卡片的语义保持一致。
function profitClass(value: number): string {
  return value < 0 ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400'
}

function metricValue(point: DashboardBusinessDailyPoint, metric: TrendMetric): number {
  switch (metric) {
    case 'requests':
      return point.total_requests
    case 'tokens':
      return point.total_tokens
    case 'consumption':
      return point.actual_cost
    case 'profit':
      return dailyProfit(point, false)
  }
}

const chartData = computed(() => {
  if (!daily.value.length) return null
  const metric = trendMetric.value
  const color = METRIC_COLORS[metric]
  const datasets = [{
    label: metric === 'profit' ? t('admin.businessHistory.profitLegendAll') : t(`admin.businessHistory.${metric}Trend`),
    data: daily.value.map((point) => metricValue(point, metric)),
    borderColor: color,
    backgroundColor: `${color}18`,
    fill: metric !== 'profit',
    tension: 0.25
  }]
  if (metric === 'profit') {
    // 排除 admin 的收益单独一条线，避免管理员体验额度把真实收益抬高。
    datasets.push({
      label: t('admin.businessHistory.profitLegendExcludingAdmin'),
      data: daily.value.map((point) => dailyProfit(point, true)),
      borderColor: '#0d9488',
      backgroundColor: '#0d948818',
      fill: false,
      tension: 0.25
    })
  }
  return {
    labels: daily.value.map((point) => formatDate(point.bucket_date)),
    datasets
  }
})

const chartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' as const },
  plugins: {
    // 收益指标同图两条线，必须显示图例才能区分口径。
    legend: { display: trendMetric.value === 'profit' },
    tooltip: {
      callbacks: {
        label: (context: { raw: unknown; dataset?: { label?: string } }) => {
          const raw = Number(context.raw ?? 0)
          if (trendMetric.value === 'profit') return `${context.dataset?.label ?? ''} ${formatMoney(raw)}`
          if (trendMetric.value === 'consumption') return formatMoney(raw)
          if (trendMetric.value === 'tokens') return formatTokens(raw)
          return `${formatNumber(raw)} ${t('admin.businessHistory.requestUnit')}`
        }
      }
    }
  },
  scales: {
    x: { ticks: { maxTicksLimit: 10 } },
    y: {
      // 收益可能为负，强制从 0 起会把亏损日压平。
      beginAtZero: trendMetric.value !== 'profit',
      title: {
        display: true,
        text: trendMetric.value === 'requests'
          ? t('admin.businessHistory.requestUnit')
          : trendMetric.value === 'tokens'
            ? t('admin.businessHistory.tokenUnit')
            : t('admin.businessHistory.consumptionUnit')
      },
      ticks: {
        callback: (raw: string | number) => isMoneyTrend.value
          ? `$${Number(raw).toLocaleString('zh-CN', { maximumFractionDigits: 2 })}`
          : trendMetric.value === 'tokens'
            ? formatTokens(Number(raw))
            : formatNumber(Number(raw))
      }
    }
  }
}))

function formatLocalDate(value: Date): string {
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}

function formatDate(value: string): string {
  return String(value).slice(0, 10)
}

function formatNumber(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(Number(value))) return '--'
  return Number(value).toLocaleString('zh-CN')
}

function formatTokens(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(Number(value))) return '--'
  const number = Number(value)
  if (number >= 1_000_000_000) return `${(number / 1_000_000_000).toFixed(2)}B`
  if (number >= 1_000_000) return `${(number / 1_000_000).toFixed(2)}M`
  if (number >= 1_000) return `${(number / 1_000).toFixed(2)}K`
  return number.toLocaleString('zh-CN')
}

function formatMoney(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(Number(value))) return '--'
  return `$${Number(value).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

async function loadSummary() {
  if (!startDate.value || !endDate.value || startDate.value > endDate.value) {
    app.showError(t('admin.businessHistory.invalidRange'))
    return
  }
  loading.value = true
  loadError.value = false
  try {
    summary.value = await adminAPI.dashboard.getBusinessSummary({ start_date: startDate.value, end_date: endDate.value })
  } catch (error) {
    loadError.value = true
    console.error('读取经营历史失败:', error)
    app.showError(t('admin.businessHistory.loadFailed'))
  } finally {
    loading.value = false
  }
}

// 页面进入时只读取永久日汇总，不触发明细扫描或后台聚合任务。
onMounted(loadSummary)
</script>
