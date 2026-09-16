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

      <!-- 实账口径与旧请求估算并列说明，不能用缺失快照补零制造精确收益。 -->
      <section class="card space-y-3 p-5 text-sm">
        <p class="font-medium text-gray-900 dark:text-white">{{ t('admin.businessHistory.ledgerDescription') }}</p>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <p>{{ t('admin.businessHistory.lifetimeRecharge') }}<strong class="ml-2">{{ formatMoney(summary?.upstream_recharge_total) }}</strong></p>
          <p>{{ t('admin.businessHistory.currentCash') }}<strong class="ml-2">{{ formatMoney(summary?.ledger?.cash_balance) }}</strong></p>
          <p>{{ t('admin.businessHistory.currentSubscription') }}<strong class="ml-2">{{ formatMoney(summary?.ledger?.subscription_balance) }}</strong></p>
          <p>{{ t('admin.businessHistory.lifetimeConsumptionEstimate') }}<strong data-test="ledger-lifetime-consumption" class="ml-2">{{ formatMoney(summary?.ledger?.lifetime_consumption_estimate) }}</strong></p>
          <p>{{ t('admin.businessHistory.lifetimeUserCharges') }}<strong class="ml-2">{{ formatMoney(summary?.lifetime.actual_cost) }}</strong></p>
          <p>{{ t('admin.businessHistory.lifetimeProfitEstimate') }}<strong class="ml-2">{{ formatMoney(summary?.ledger?.lifetime_profit_estimate) }}</strong></p>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.zeroOpeningEstimate') }}</p>
        <p v-if="summary?.ledger" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.assetCompleteness', { amount: formatMoney(summary.ledger.known_balance_subtotal), count: summary.ledger.unknown_sites }) }}</p>
        <!-- 顶部累计口径不依赖区间边界，日期筛选仅控制请求统计及每日明细。 -->
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.currentTotalsScope') }}</p>
      </section>

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
                <span>{{ formatMoney(summary?.lifetime.actual_cost) }}</span>
                <span class="text-gray-300 dark:text-dark-500">/</span>
                <span class="text-emerald-600 dark:text-emerald-400">{{ formatMoney(summary?.lifetime.actual_cost_excluding_admin) }}</span>
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

        <button type="button" data-test="open-recharge-details" class="card p-5 text-left transition hover:ring-2 hover:ring-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500" @click="openDetails('recharge')">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-red-100 p-2 dark:bg-red-900/30"><Icon name="dollar" size="md" class="text-red-600 dark:text-red-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamRechargeTotal') }}</p>
              <p data-test="upstream-recharge-total" class="mt-1 text-2xl font-bold text-red-600 dark:text-red-400">
                {{ formatMoney(summary?.upstream_recharge_total) }}
              </p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamRechargeDescription') }}</p>
            </div>
          </div>
        </button>

        <button type="button" data-test="open-balance-details" class="card p-5 text-left transition hover:ring-2 hover:ring-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500" @click="openDetails('balance')">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-sky-100 p-2 dark:bg-sky-900/30"><Icon name="database" size="md" class="text-sky-600 dark:text-sky-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamBalanceTotal') }}</p>
              <p data-test="upstream-balance-total" class="mt-1 text-2xl font-bold text-sky-600 dark:text-sky-400">{{ formatMoney(summary?.ledger?.total_balance) }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamBalanceDescription') }}</p>
              <p data-test="upstream-balance-snapshot-hint" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.currentBalanceHint') }}</p>
            </div>
          </div>
        </button>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-orange-100 p-2 dark:bg-orange-900/30"><Icon name="chart" size="md" class="text-orange-600 dark:text-orange-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamTotalConsumption') }}</p>
              <p data-test="upstream-total-consumption" class="mt-1 text-2xl font-bold text-orange-600 dark:text-orange-400">{{ formatMoney(upstreamTotalConsumption) }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamTotalConsumptionFormula') }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.requestCostReference') }} {{ formatMoney(summary?.range.upstream_cost) }}</p>
            </div>
          </div>
        </article>

        <button type="button" data-test="open-profit-details" class="card p-5 text-left transition hover:ring-2 hover:ring-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500" @click="openDetails('profit')">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-violet-100 p-2 dark:bg-violet-900/30"><Icon name="bolt" size="md" class="text-violet-600 dark:text-violet-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalProfit') }}</p>
              <p data-test="total-profit" class="mt-1 flex flex-wrap items-baseline gap-2 text-2xl font-bold text-gray-900 dark:text-white">
                <span>{{ formatMoney(totalProfitAllAccounts) }}</span>
              </p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalProfitFormula') }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.requestProfitReference') }} {{ formatMoney(requestProfitAllAccounts) }} / {{ formatMoney(totalProfitUsers) }}</p>
            </div>
          </div>
        </button>
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
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.requestEstimateNotice') }}</p>
        <div class="overflow-x-auto border-y border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[1400px] text-base tabular-nums">
            <thead class="whitespace-nowrap bg-gray-50 text-left text-sm font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2">{{ t('admin.businessHistory.date') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.totalRequests') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.totalTokens') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.chargesAllShort') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.chargesUsersShort') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.costAllShort') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.costUsersShort') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.profitAllShort') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.profitUsersShort') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.cumulativeProfit') }}</th>
                <th class="px-3 py-2 text-right">{{ t('admin.businessHistory.effectiveRecharge') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="point in dailyDescending" :key="point.bucket_date" data-test="history-daily-row" class="text-gray-700 dark:text-gray-200">
                <td class="whitespace-nowrap px-3 py-3 font-medium">{{ formatDate(point.bucket_date) }}</td>
                <td class="px-3 py-3 text-right">{{ formatNumber(point.total_requests) }}</td>
                <td class="px-3 py-3 text-right">{{ formatTokens(point.total_tokens) }}</td>
                <td class="px-3 py-3 text-right">{{ formatMoney(point.actual_cost) }}</td>
                <td class="px-3 py-3 text-right text-emerald-600 dark:text-emerald-400">{{ formatMoney(point.actual_cost_excluding_admin) }}</td>
                <td class="px-3 py-3 text-right text-red-600 dark:text-red-400">{{ formatMoney(point.upstream_cost) }}</td>
                <td class="px-3 py-3 text-right text-red-600 dark:text-red-400">{{ formatMoney(point.upstream_cost_excluding_admin) }}</td>
                <td class="px-3 py-3 text-right font-medium" :class="profitClass(dailyProfit(point, false))" data-test="history-daily-profit-all">{{ formatMoney(dailyProfit(point, false)) }}</td>
                <td class="px-3 py-3 text-right font-medium" :class="profitClass(dailyProfit(point, true))" data-test="history-daily-profit-users">{{ formatMoney(dailyProfit(point, true)) }}</td>
                <td class="px-3 py-3 text-right font-medium" data-test="history-cumulative-profit" :title="profitSnapshotHint(profitByDate.get(formatDate(point.bucket_date)))">{{ formatMoney(profitByDate.get(formatDate(point.bucket_date))?.cumulative_profit) }}</td>
                <td class="px-3 py-3 text-right">{{ formatMoney(point.recharge_amount) }}</td>
              </tr>
              <tr v-if="!loading && daily.length === 0"><td colspan="11" class="px-3 py-10 text-center text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.cumulativeNotice') }}</p>
    </div>
    <BaseDialog :show="detailKind !== null" :title="detailTitle" width="wide" @close="detailKind = null">
      <div v-if="detailsLoading" class="flex justify-center p-8"><LoadingSpinner /></div>
      <div v-else-if="detailsError" class="space-y-3 text-sm text-red-600">
        <p>{{ t('admin.businessHistory.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary" @click="detailKind && openDetails(detailKind)">{{ t('common.refresh') }}</button>
      </div>
      <template v-else-if="detailKind === 'balance'">
        <p class="mb-4 text-sm text-gray-500">{{ t('admin.businessHistory.upstreamBalanceDescription') }}</p>
        <div class="overflow-x-auto">
          <table class="w-full whitespace-nowrap text-sm tabular-nums">
            <thead><tr class="border-b text-left"><th class="p-3">{{ t('admin.businessHistory.site') }}</th><th class="p-3 text-right">{{ t('admin.businessHistory.cash') }}</th><th class="p-3 text-right">{{ t('admin.businessHistory.subscription') }}</th><th class="p-3 text-right">{{ t('admin.businessHistory.balance') }}</th></tr></thead>
            <tbody><tr v-for="asset in balanceAssets" :key="asset.site_id" class="border-b dark:border-dark-700" data-test="balance-detail-row"><td class="p-3">{{ asset.site_name }}<small class="block text-gray-500">{{ asset.domain }}</small></td><td class="p-3 text-right">{{ formatMoney(asset.cash_balance) }}</td><td class="p-3 text-right">{{ formatMoney(asset.subscription_balance) }}</td><td class="p-3 text-right font-semibold">{{ formatMoney(asset.total_balance) }}</td></tr></tbody>
            <tfoot><tr><th class="p-3 text-left" colspan="3">{{ t('admin.businessHistory.total') }}</th><td class="p-3 text-right font-bold" data-test="balance-detail-total">{{ formatMoney(detailBalanceTotal) }}</td></tr></tfoot>
          </table>
        </div>
        <p v-if="!balanceAssets.length" class="p-4 text-center text-gray-500">{{ t('admin.businessHistory.noData') }}</p>
      </template>
      <template v-else-if="detailKind === 'recharge'">
        <p class="mb-4 font-semibold">{{ t('admin.businessHistory.total') }}：{{ formatMoney(rechargeDetails?.total_amount) }}</p>
        <details v-for="site in rechargeDetails?.sites" :key="site.site_id ?? site.site_name" class="mb-3 rounded-lg border p-3 dark:border-dark-700" open>
          <summary class="cursor-pointer font-medium">{{ site.site_name }} <span class="ml-3 tabular-nums">{{ formatMoney(site.total_amount) }}</span></summary>
          <ul class="mt-2 divide-y dark:divide-dark-700">
            <li v-for="event in site.items" :key="event.id ?? event.source_key" class="flex flex-wrap items-center justify-between gap-2 py-3 text-sm" data-test="recharge-detail-row">
              <div><span>{{ formatTimestamp(event.occurred_at) }}</span><span class="ml-2 text-gray-500">{{ event.record_type === 'subscription' ? t('admin.businessHistory.subscriptionPurchase') : t('admin.businessHistory.recharge') }}</span><p v-if="event.note" class="mt-1 break-all text-xs text-gray-500">{{ event.note }}</p></div>
              <strong class="tabular-nums">{{ formatMoney(event.amount) }}</strong>
            </li>
          </ul>
        </details>
        <p v-if="!rechargeDetails?.sites.length" class="p-4 text-center text-gray-500">{{ t('admin.businessHistory.noData') }}</p>
      </template>
      <template v-else-if="detailKind === 'profit'">
        <p class="mb-4 text-sm text-gray-500">{{ t('admin.businessHistory.cumulativeNotice') }}</p>
        <div class="overflow-x-auto">
          <table class="w-full whitespace-nowrap text-base tabular-nums">
            <thead><tr class="border-b text-left text-sm"><th class="p-3">{{ t('admin.businessHistory.date') }}</th><th class="p-3 text-right">{{ t('admin.businessHistory.dailyProfit') }}</th><th class="p-3 text-right">{{ t('admin.businessHistory.cumulativeProfit') }}</th></tr></thead>
            <tbody><tr v-for="point in profitHistory" :key="point.bucket_date" class="border-b dark:border-dark-700" data-test="profit-detail-row"><td class="p-3">{{ point.bucket_date }}<small class="block text-xs text-gray-500">{{ profitSnapshotHint(point) }}</small></td><td class="p-3 text-right">{{ formatMoney(point.daily_profit) }}</td><td class="p-3 text-right font-semibold">{{ formatMoney(point.cumulative_profit) }}</td></tr></tbody>
          </table>
        </div>
        <p v-if="!profitHistory.length" class="p-4 text-center text-gray-500">{{ t('admin.businessHistory.noProfitHistory') }}</p>
      </template>
    </BaseDialog>
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
import type { DashboardBusinessDailyPoint, DashboardBusinessProfitPoint, DashboardBusinessSummary } from '@/api/admin/dashboard'
import { balanceCenterAPI, type BalanceCenterAsset, type BalanceCenterRechargeSummary } from '@/api/admin/balanceCenter'
import BaseDialog from '@/components/common/BaseDialog.vue'
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
const dailyDescending = computed(() => [...daily.value].reverse())
const profitHistory = computed(() => [...(summary.value?.profit_history ?? [])].sort((a, b) => b.bucket_date.localeCompare(a.bucket_date)))
const profitByDate = computed(() => new Map(profitHistory.value.map(point => [point.bucket_date, point])))
type DetailKind = 'balance' | 'recharge' | 'profit'
const detailKind = ref<DetailKind | null>(null)
const detailsLoading = ref(false)
const detailsError = ref(false)
const balanceAssets = ref<BalanceCenterAsset[]>([])
const rechargeDetails = ref<BalanceCenterRechargeSummary | null>(null)
const detailTitle = computed(() => t(`admin.businessHistory.${detailKind.value === 'balance' ? 'balanceDetails' : detailKind.value === 'recharge' ? 'rechargeDetails' : 'profitDetails'}`))
const detailBalanceTotal = computed(() => balanceAssets.value.length && balanceAssets.value.every(asset => asset.balance_known && asset.total_balance != null)
  ? balanceAssets.value.reduce((sum, asset) => sum + asset.total_balance!, 0) : null)
let detailsRequest = 0

async function openDetails(kind: DetailKind) {
  const request = ++detailsRequest
  detailKind.value = kind
  detailsError.value = false
  detailsLoading.value = kind !== 'profit'
  try {
    if (kind === 'balance') {
      const assets = await balanceCenterAPI.assets()
      if (request === detailsRequest) balanceAssets.value = assets
    } else if (kind === 'recharge') {
      const recharges = await balanceCenterAPI.rechargeSummary({})
      if (request === detailsRequest) rechargeDetails.value = recharges
    }
  } catch {
    if (request === detailsRequest) detailsError.value = true
  } finally {
    if (request === detailsRequest) detailsLoading.value = false
  }
}

function formatTimestamp(value: string): string {
  return new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
}

function profitSnapshotHint(point?: DashboardBusinessProfitPoint): string {
  if (!point || point.status === 'missing_snapshot') return t('admin.businessHistory.missingProfitSnapshot')
  return point.status === 'current' ? t('admin.businessHistory.asOfNow') : t('admin.businessHistory.snapshotTime', { time: point.captured_at ? formatTimestamp(point.captured_at) : '--' })
}
// 暂按用户确认的累计公式计算，不能混用区间充值与当前余额。
const upstreamTotalConsumption = computed(() => {
  if (!summary.value || summary.value.ledger?.total_balance == null) return null
  return summary.value.upstream_recharge_total - summary.value.ledger.total_balance
})
const totalProfitAllAccounts = computed(() => {
  if (!summary.value || upstreamTotalConsumption.value == null) return null
  return summary.value.lifetime.actual_cost - upstreamTotalConsumption.value
})
// 请求估算无法精确分摊实账成本，只在参考文案下保留两个旧口径。
const requestProfitAllAccounts = computed(() => {
  if (!summary.value) return null
  return summary.value.range.actual_cost - summary.value.range.upstream_cost
})
const totalProfitUsers = computed(() => {
  if (!summary.value) return null
  return summary.value.range.actual_cost_excluding_admin - summary.value.range.upstream_cost_excluding_admin
})
// 余额快照按日采集，所选结束日可能未采集到，此时提示“暂无快照”而不是展示误导性的零余额。
const balanceSnapshotHint = computed(() => {
  if (!summary.value) return ''
  const snapshotDate = summary.value.range_balance_snapshot_date
  if (!snapshotDate) return t('admin.businessHistory.balanceSnapshotEmpty')
  return t('admin.businessHistory.balanceSnapshotAsOf', { date: formatDate(snapshotDate) })
})
// 金额按确认的人民币 1:1 口径展示，不作美元汇率换算。
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
          ? `¥${Number(raw).toLocaleString('zh-CN', { maximumFractionDigits: 2 })}`
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
  return `¥${Number(value).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
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
