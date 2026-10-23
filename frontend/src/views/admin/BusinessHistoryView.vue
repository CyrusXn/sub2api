<template>
  <AppLayout>
    <div data-test="business-history-content" class="space-y-6">
      <section class="flex flex-wrap items-center gap-3 border-b border-gray-200 pb-5 dark:border-dark-700" aria-label="经营历史查询">
        <fieldset class="flex min-w-0 items-center gap-2" :disabled="loading">
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.dashboard.timeRange') }}:</span>
          <DateRangePicker
            v-model:start-date="startDate"
            v-model:end-date="endDate"
            :excluded-presets="['last24Hours']"
            @change="loadSummary"
          />
        </fieldset>
        <button type="button" class="btn btn-secondary h-10 w-10 p-0" :title="t('common.refresh')" :disabled="loading" @click="loadSummary">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
        <button data-test="history-all" type="button" class="btn btn-secondary h-10" :disabled="loading" @click="loadAll">{{ t('admin.businessHistory.allDates') }}</button>
      </section>

      <p v-if="loadError" class="rounded border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300">
        {{ t('admin.businessHistory.loadFailed') }}
      </p>

      <!-- 全部恢复原资产账本累计口径，历史区间仍保留资产快照算法。 -->
      <section class="card space-y-3 p-5 text-sm">
        <p class="font-medium text-gray-900 dark:text-white">{{ t('admin.businessHistory.ledgerDescription') }}</p>
        <p data-test="applied-range">{{ queriedRange ? `${queriedRange.start_date} ～ ${queriedRange.end_date}` : t('admin.businessHistory.allDates') }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.currentTotalsScope') }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.userRechargeDescription') }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamBalanceDescription') }}</p>
        <p v-if="!allDates">{{ t('admin.businessHistory.boundaryBalances', { opening: formatMoney(summary?.ledger?.opening_balance), closing: formatMoney(summary?.ledger?.closing_balance) }) }}</p>
        <p data-test="ledger-status" class="text-xs text-gray-500 dark:text-gray-400">{{ ledgerStatusHint }}</p>
        <p v-if="readOnlyPreview" data-test="read-only-preview" class="text-xs text-amber-600">{{ t('admin.businessHistory.readOnlyPreview') }}</p>
      </section>

      <section class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4" aria-label="经营指标">
        <article class="card p-5">
          <div class="flex items-center gap-3">
            <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30"><Icon name="document" size="md" class="text-blue-600 dark:text-blue-400" /></div>
            <div>
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalRequests') }}</p>
              <p data-test="range-requests" class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ formatNumber(totals?.total_requests) }}</p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30"><Icon name="cube" size="md" class="text-amber-600 dark:text-amber-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.totalTokens') }}</p>
              <p data-test="range-tokens" class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ formatTokens(totals?.total_tokens) }}</p>
              <p data-test="token-breakdown" class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
                {{ t('admin.businessHistory.inputTokens') }} {{ formatTokens(totals?.input_tokens) }} ·
                {{ t('admin.businessHistory.outputTokens') }} {{ formatTokens(totals?.output_tokens) }} ·
                {{ t('admin.businessHistory.cacheCreationTokens') }} {{ formatTokens(totals?.cache_creation_tokens) }} ·
                {{ t('admin.businessHistory.cacheReadTokens') }} {{ formatTokens(totals?.cache_read_tokens) }}
              </p>
            </div>
          </div>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-emerald-100 p-2 dark:bg-emerald-900/30"><Icon name="dollar" size="md" class="text-emerald-600 dark:text-emerald-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t(`admin.businessHistory.${allDates ? 'lifetimeSiteConsumption' : 'totalConsumption'}`) }}</p>
              <p data-test="range-consumption" class="mt-1 flex flex-wrap items-baseline gap-2 text-2xl font-bold text-gray-900 dark:text-white">
                <span>{{ formatMoney(totals?.actual_cost) }}</span>
                <span class="text-gray-300 dark:text-dark-500">/</span>
                <span class="text-emerald-600 dark:text-emerald-400">{{ formatMoney(totals?.actual_cost_excluding_admin) }}</span>
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
              <p data-test="user-total-recharge" class="mt-2 text-xl font-bold text-emerald-600 dark:text-emerald-400">{{ formatMoney(totals?.recharge_amount) }}</p>
            </div>
            <div class="min-w-0 border-l border-gray-200 pl-4 dark:border-dark-700">
              <div class="flex items-center gap-2">
                <Icon name="dollar" size="sm" class="shrink-0 text-teal-600 dark:text-teal-400" />
                <p class="truncate text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.userBalanceTotal') }}</p>
              </div>
              <p data-test="user-balance-total" class="mt-2 text-xl font-bold text-teal-600 dark:text-teal-400">{{ formatMoney(userBalanceTotal) }}</p>
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
                {{ formatMoney(upstreamRechargeTotal) }}
              </p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.businessHistory.${allDates ? 'lifetimeRecharge' : 'upstreamRechargeDescription'}`) }}</p>
            </div>
          </div>
        </button>

        <article class="card relative p-5">
          <button data-test="refresh-upstream-balance" type="button" class="absolute right-3 top-3 rounded p-2 hover:bg-gray-100 dark:hover:bg-dark-700 disabled:opacity-50" :disabled="balancesLoading" :aria-label="t('admin.businessHistory.refreshBalances')" :title="t('admin.businessHistory.refreshBalances')" @click="refreshBalances">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': balancesLoading }" />
          </button>
          <button type="button" data-test="open-balance-details" class="w-full text-left" @click="openDetails('balance')">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-sky-100 p-2 dark:bg-sky-900/30"><Icon name="database" size="md" class="text-sky-600 dark:text-sky-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="pr-8 text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamBalanceTotal') }}</p>
              <p data-test="upstream-balance-total" class="mt-1 text-2xl font-bold text-sky-600 dark:text-sky-400">{{ formatMoney(upstreamBalanceTotal) }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.upstreamBalanceDescription') }}</p>
              <p data-test="upstream-balance-snapshot-hint" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ upstreamBalanceSnapshotHint }}</p>
              <p v-if="balancesError" data-test="balance-refresh-error" role="alert" class="mt-1 text-xs text-red-600 dark:text-red-400">{{ t('admin.businessHistory.balanceRefreshFailed', { count: balances?.failed_accounts ?? 0 }) }}</p>
            </div>
          </div>
          </button>
        </article>

        <article class="card p-5">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-orange-100 p-2 dark:bg-orange-900/30"><Icon name="chart" size="md" class="text-orange-600 dark:text-orange-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t(`admin.businessHistory.${allDates ? 'lifetimeUpstreamConsumption' : 'upstreamTotalConsumption'}`) }}</p>
              <p data-test="upstream-total-consumption" class="mt-1 text-2xl font-bold text-orange-600 dark:text-orange-400">{{ formatMoney(upstreamTotalConsumption) }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ usesRechargeBalanceFallback ? rechargeBalanceFallbackHint : t(`admin.businessHistory.${allDates ? 'lifetimeConsumptionFormula' : usesRequestEstimate ? 'requestCostNotice' : 'upstreamTotalConsumptionFormula'}`) }}</p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.businessHistory.requestCostReference') }} {{ formatMoney(totals?.upstream_cost) }}</p>
            </div>
          </div>
        </article>

        <button type="button" data-test="open-profit-details" class="card p-5 text-left transition hover:ring-2 hover:ring-primary-400 focus-visible:ring-2 focus-visible:ring-primary-500" @click="openDetails('profit')">
          <div class="flex items-start gap-3">
            <div class="rounded-lg bg-violet-100 p-2 dark:bg-violet-900/30"><Icon name="bolt" size="md" class="text-violet-600 dark:text-violet-400" /></div>
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t(`admin.businessHistory.${allDates ? 'lifetimeProfit' : 'totalProfit'}`) }}</p>
              <p data-test="total-profit" class="mt-1 flex flex-wrap items-baseline gap-2 text-2xl font-bold text-gray-900 dark:text-white">
                <span>{{ formatMoney(totalProfitAllAccounts) }}</span>
              </p>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ usesRechargeBalanceFallback ? rechargeBalanceFallbackHint : t(`admin.businessHistory.${allDates ? 'lifetimeProfitFormula' : usesRequestEstimate ? 'requestCostNotice' : 'totalProfitFormula'}`) }}</p>
              <div class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-700">
                <p class="text-sm text-gray-500 dark:text-gray-400">{{ t(`admin.businessHistory.${allDates ? 'lifetimeRechargeDifference' : 'rechargeDifference'}`) }}</p>
                <p data-test="recharge-difference" class="mt-1 text-xl font-bold" :class="rechargeDifference == null ? '' : profitClass(rechargeDifference)">{{ formatMoney(rechargeDifference) }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.businessHistory.${allDates ? 'lifetimeRechargeDifferenceDescription' : 'rechargeDifferenceDescription'}`) }}</p>
              </div>
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
            <tfoot v-if="daily.length" class="border-t-2 border-gray-300 bg-gray-50 font-semibold dark:border-dark-600 dark:bg-dark-800">
              <tr data-test="history-total-row">
                <td class="px-3 py-3">{{ t('admin.businessHistory.total') }}</td>
                <td class="px-3 py-3 text-right">{{ formatNumber(dailyTotals.total_requests) }}</td>
                <td class="px-3 py-3 text-right">{{ formatTokens(dailyTotals.total_tokens) }}</td>
                <td class="px-3 py-3 text-right">{{ formatMoney(dailyTotals.actual_cost) }}</td>
                <td class="px-3 py-3 text-right text-emerald-600">{{ formatMoney(dailyTotals.actual_cost_excluding_admin) }}</td>
                <td class="px-3 py-3 text-right text-red-600">{{ formatMoney(dailyTotals.upstream_cost) }}</td>
                <td class="px-3 py-3 text-right text-red-600">{{ formatMoney(dailyTotals.upstream_cost_excluding_admin) }}</td>
                <td class="px-3 py-3 text-right" :class="profitClass(dailyTotals.profit)">{{ formatMoney(dailyTotals.profit) }}</td>
                <td class="px-3 py-3 text-right" :class="profitClass(dailyTotals.profit_users)">{{ formatMoney(dailyTotals.profit_users) }}</td>
                <td class="px-3 py-3 text-right" data-test="history-total-cumulative">{{ formatMoney(profitByDate.get(formatDate(dailyDescending[0].bucket_date))?.cumulative_profit) }}</td>
                <td class="px-3 py-3 text-right">{{ formatMoney(dailyTotals.recharge_amount) }}</td>
              </tr>
            </tfoot>
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
        <p v-if="balancesError" role="alert" class="mb-3 text-sm text-red-600">{{ t('admin.businessHistory.balanceRefreshFailed', { count: balances?.failed_accounts ?? 0 }) }}</p>
        <div class="overflow-x-auto">
          <table class="w-full whitespace-nowrap text-sm tabular-nums">
            <thead><tr class="border-b text-left"><th class="p-3">{{ t('admin.businessHistory.site') }}</th><th class="p-3 text-right">{{ t('admin.businessHistory.minimumAccountBalance') }}</th></tr></thead>
            <tbody><tr v-for="site in balances?.sites" :key="site.domain" class="border-b dark:border-dark-700" data-test="balance-detail-row"><td class="p-3">{{ site.domain }}<small v-if="site.failed_accounts" class="block text-amber-600">{{ t('admin.businessHistory.balanceAccountsFailed', { count: site.failed_accounts }) }}</small></td><td class="p-3 text-right font-semibold">{{ formatMoney(site.balance) }}</td></tr></tbody>
          </table>
          <p v-if="!balances?.sites.length" class="p-4 text-center text-gray-500">{{ t('admin.businessHistory.noData') }}</p>
        </div>
        <p v-if="!allDates" class="mt-3 text-sm text-gray-500">{{ t('admin.businessHistory.boundaryBalances', { opening: formatMoney(summary?.ledger?.opening_balance), closing: formatMoney(summary?.ledger?.closing_balance) }) }}</p>
        <p class="mt-3 text-sm text-gray-500">{{ ledgerStatusHint }}</p>
        <p class="mt-4 font-bold" data-test="balance-detail-total">{{ formatMoney(upstreamBalanceTotal) }}</p>
        <p class="mt-2 text-sm text-gray-500">{{ upstreamBalanceSnapshotHint }}</p>
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
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
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
import type { BusinessBalanceRefresh } from '@/api/admin/accounts'
import { balanceCenterAPI, type BalanceCenterRechargeSummary } from '@/api/admin/balanceCenter'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Icon from '@/components/icons/Icon.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { useAppStore } from '@/stores/app'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, Filler)

const { t } = useI18n()
const app = useAppStore()
type TrendMetric = 'requests' | 'tokens' | 'consumption' | 'profit'

// 默认保留原有起点及北京时间今天；控件草稿只有点击应用后才查询。
const defaultRange = { start_date: '2026-07-09', end_date: new Date().toLocaleDateString('en-CA', { timeZone: 'Asia/Shanghai' }) }
const startDate = ref(defaultRange.start_date)
const endDate = ref(defaultRange.end_date)
const summary = ref<DashboardBusinessSummary | null>(null)
const queriedRange = ref<{ start_date: string; end_date: string } | null>({ ...defaultRange })
const loading = ref(false)
const allDates = computed(() => queriedRange.value == null)
const totals = computed(() => allDates.value ? summary.value?.lifetime : summary.value?.range)
const upstreamRechargeTotal = computed(() => allDates.value ? summary.value?.upstream_recharge_total : summary.value?.range_upstream_recharge_total)
const readOnlyPreview = import.meta.env.VITE_READ_ONLY_PREVIEW === 'true'
const balances = ref<BusinessBalanceRefresh | null>(null)
const balancesLoading = ref(false)
const balancesError = ref(false)
const upstreamBalanceTotal = computed(() => balances.value?.total_balance)
// 总余额卡片独立查询全量账号；历史消费收益继续使用对应账本资产，不混入今天的余额。
const ledgerBalance = computed(() => allDates.value ? summary.value?.ledger?.total_balance : summary.value?.ledger?.closing_balance)
const userBalanceTotal = computed(() => allDates.value ? summary.value?.user_balance_total : summary.value?.range_user_balance_total)
const loadError = ref(false)
const trendMetric = ref<TrendMetric>('requests')
const trendMetrics: TrendMetric[] = ['requests', 'tokens', 'consumption', 'profit']
const selectedMetricClass = 'bg-gray-900 text-white dark:bg-gray-100 dark:text-gray-900'
const normalMetricClass = 'text-gray-500 hover:bg-gray-50 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-gray-200'

const daily = computed(() => [...(summary.value?.daily ?? [])].sort((a, b) => a.bucket_date.localeCompare(b.bucket_date)))
const dailyDescending = computed(() => [...daily.value].reverse())
const allDaily = ref<DashboardBusinessDailyPoint[]>([])
const profitHistory = computed(() => {
  const requests = new Map(allDaily.value.map(point => [formatDate(point.bucket_date), point]))
  for (const point of daily.value) requests.set(formatDate(point.bucket_date), point)
  const ordered = [...requests.entries()].sort(([a], [b]) => a.localeCompare(b))
  return [...(summary.value?.profit_history ?? [])].map(point => {
    // 仅全部模式的当天累计跟随原资产账本；已保存的历史快照及日期区间算法不变。
    const today = new Date().toLocaleDateString('en-CA', { timeZone: 'Asia/Shanghai' })
    if (allDates.value && point.bucket_date === today) point = { ...point, cumulative_profit: totalProfitAllAccounts.value }
    const request = requests.get(point.bucket_date)
    if (point.daily_profit != null && point.cumulative_profit != null) return point
    return {
      ...point,
      daily_profit: point.daily_profit ?? (request ? request.profit_estimate ?? request.actual_cost - request.upstream_cost : null),
      cumulative_profit: allDates.value && point.bucket_date === today ? totalProfitAllAccounts.value : point.cumulative_profit ?? (allDaily.value.length ? ordered.filter(([date]) => date <= point.bucket_date).reduce((total, [, row]) => total + (row.profit_estimate ?? row.actual_cost - row.upstream_cost), 0) : null),
      status: 'usage_estimate' as const
    }
  })
  .filter(point => !queriedRange.value || (point.bucket_date >= queriedRange.value.start_date && point.bucket_date <= queriedRange.value.end_date))
  .sort((a, b) => b.bucket_date.localeCompare(a.bucket_date))
})
const profitByDate = computed(() => new Map(profitHistory.value.map(point => [point.bucket_date, point])))
type DetailKind = 'balance' | 'recharge' | 'profit'
const detailKind = ref<DetailKind | null>(null)
const detailsLoading = ref(false)
const detailsError = ref(false)
const rechargeDetails = ref<BalanceCenterRechargeSummary | null>(null)
const detailTitle = computed(() => t(`admin.businessHistory.${detailKind.value === 'balance' ? 'balanceDetails' : detailKind.value === 'recharge' ? 'rechargeDetails' : 'profitDetails'}`))
let detailsRequest = 0

async function openDetails(kind: DetailKind) {
  const request = ++detailsRequest
  detailKind.value = kind
  detailsError.value = false
  detailsLoading.value = kind === 'recharge'
  try {
    if (kind === 'recharge') {
      // 余额中心结束时间为闭区间，保留 PostgreSQL 微秒精度以免漏掉日末记录。
      const recharges = await balanceCenterAPI.rechargeSummary(queriedRange.value ? {
        start_time: `${queriedRange.value.start_date}T00:00:00+08:00`,
        end_time: `${queriedRange.value.end_date}T23:59:59.999999+08:00`
      } : {})
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
  if (point?.status === 'usage_estimate') return t('admin.businessHistory.frozenUsageEstimate')
  if (!point || point.status === 'missing_snapshot') return t('admin.businessHistory.missingProfitSnapshot')
  return point.status === 'current' ? t('admin.businessHistory.asOfNow') : t('admin.businessHistory.snapshotTime', { time: point.captured_at ? formatTimestamp(point.captured_at) : '--' })
}
// 有日期筛选时，经营历史与使用记录共用请求结算的上游成本；资产账本只保留“全部”口径。
const usesRequestEstimate = computed(() => !allDates.value && summary.value != null)
const rangeUpstreamConsumption = computed(() => totals.value?.upstream_cost ?? daily.value.reduce((total, point) => total + point.upstream_cost, 0))
// 仅请求成本超出所选区间充值时切换用户指定的差额口径；资产快照估值不参与这个判断。
const usesRechargeBalanceFallback = computed(() => !allDates.value && upstreamRechargeTotal.value != null && rangeUpstreamConsumption.value > upstreamRechargeTotal.value)
const rechargeBalanceFallbackHint = computed(() => t(`admin.businessHistory.${ledgerBalance.value == null ? 'rechargeBalanceFallbackMissing' : 'rechargeBalanceFallbackNotice'}`))
const upstreamTotalConsumption = computed(() => {
  if (!summary.value) return null
  if (allDates.value || usesRechargeBalanceFallback.value) return ledgerBalance.value == null || upstreamRechargeTotal.value == null ? null : upstreamRechargeTotal.value - ledgerBalance.value
  return rangeUpstreamConsumption.value
})
const totalProfitAllAccounts = computed(() => {
  if (!summary.value) return null
  if (allDates.value || usesRechargeBalanceFallback.value) return upstreamTotalConsumption.value == null ? null : totals.value!.actual_cost - upstreamTotalConsumption.value
  return totals.value!.actual_cost - rangeUpstreamConsumption.value
})
const rechargeDifference = computed(() => summary.value
  ? (totals.value?.recharge_amount ?? 0) - (upstreamRechargeTotal.value ?? 0) : null)
const ledgerStatusHint = computed(() => {
  if (allDates.value) return t('admin.businessHistory.zeroOpeningEstimate')
  if (usesRechargeBalanceFallback.value) return rechargeBalanceFallbackHint.value
  if (usesRequestEstimate.value) return t('admin.businessHistory.requestCostNotice')
  const ledger = summary.value?.ledger
  if (!ledger || ledger.range_status === 'missing_boundary') return t('admin.businessHistory.ledgerMissingBoundary')
  if (ledger.range_status === 'site_scope_changed') return t('admin.businessHistory.ledgerScopeChanged')
  return t(`admin.businessHistory.${ledger.range_status === 'available' ? 'ledgerAvailable' : 'ledgerInexactBoundary'}`, {
    opening: ledger.opening_captured_at ? formatTimestamp(ledger.opening_captured_at) : '--',
    closing: ledger.closing_captured_at ? formatTimestamp(ledger.closing_captured_at) : '--'
  })
})
const upstreamBalanceSnapshotHint = computed(() => {
  if (balancesLoading.value) return t('admin.businessHistory.balanceRefreshing')
  if (!balances.value?.refreshed_at) return t('admin.businessHistory.balanceNotRefreshed')
  return t(`admin.businessHistory.${balances.value.preview_only ? 'balancePreviewSnapshot' : 'balanceRefreshedAt'}`, { time: formatTimestamp(balances.value.refreshed_at) })
})
// 请求估算无法精确分摊实账成本，只在参考文案下保留两个旧口径。
const requestProfitAllAccounts = computed(() => {
  if (!summary.value) return null
  return (totals.value?.actual_cost ?? 0) - (totals.value?.upstream_cost ?? 0)
})
const totalProfitUsers = computed(() => {
  if (!summary.value) return null
  return (totals.value?.actual_cost_excluding_admin ?? 0) - (totals.value?.upstream_cost_excluding_admin ?? 0)
})
// 余额快照按日采集，所选结束日可能未采集到，此时提示“暂无快照”而不是展示误导性的零余额。
const balanceSnapshotHint = computed(() => {
  if (!summary.value) return ''
  if (allDates.value) return t('admin.businessHistory.asOfNow')
  if (summary.value.range_user_balance_total == null) return t('admin.businessHistory.balanceSnapshotEmpty')
  if (summary.value.range_balance_captured_at) return t('admin.businessHistory.snapshotTime', { time: formatTimestamp(summary.value.range_balance_captured_at) })
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
    : point.profit_estimate ?? point.actual_cost - point.upstream_cost
}

const dailyTotals = computed(() => daily.value.reduce((total, point) => ({
  total_requests: total.total_requests + point.total_requests,
  total_tokens: total.total_tokens + point.total_tokens,
  actual_cost: total.actual_cost + point.actual_cost,
  actual_cost_excluding_admin: total.actual_cost_excluding_admin + point.actual_cost_excluding_admin,
  upstream_cost: total.upstream_cost + point.upstream_cost,
  upstream_cost_excluding_admin: total.upstream_cost_excluding_admin + point.upstream_cost_excluding_admin,
  profit: total.profit + dailyProfit(point, false),
  profit_users: total.profit_users + dailyProfit(point, true),
  recharge_amount: total.recharge_amount + point.recharge_amount
}), { total_requests: 0, total_tokens: 0, actual_cost: 0, actual_cost_excluding_admin: 0, upstream_cost: 0, upstream_cost_excluding_admin: 0, profit: 0, profit_users: 0, recharge_amount: 0 }))

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

let summaryRequest = 0
let balancesRequest = 0
onBeforeUnmount(() => { ++summaryRequest; ++detailsRequest; ++balancesRequest })

async function refreshBalances() {
  if (balancesLoading.value) return
  const request = ++balancesRequest
  balancesLoading.value = true
  balancesError.value = false
  balances.value = null
  try {
    const result = await adminAPI.accounts.refreshBusinessBalances()
    if (request !== balancesRequest) return
    balances.value = result
    balancesError.value = result.total_balance == null
  } catch {
    if (request === balancesRequest) balancesError.value = true
  } finally {
    if (request === balancesRequest) balancesLoading.value = false
  }
}

async function loadAll() {
  startDate.value = ''
  endDate.value = ''
  await loadSummary()
}

async function loadSummary() {
  if (Boolean(startDate.value) !== Boolean(endDate.value) || startDate.value > endDate.value) {
    app.showError(t('admin.businessHistory.invalidRange'))
    return
  }
  await fetchSummary(startDate.value ? { start_date: startDate.value, end_date: endDate.value } : null)
}

async function fetchSummary(range: { start_date: string; end_date: string } | null) {
  loading.value = true
  loadError.value = false
  const request = ++summaryRequest
  try {
    const [result, fullHistory] = await Promise.all([
      range ? adminAPI.dashboard.getBusinessSummary(range) : adminAPI.dashboard.getBusinessSummary(),
      // 默认不再先查全部，仍需全历史明细为缺失累计收益兜底；辅助查询失败不阻断当前区间。
      range && !allDaily.value.length ? adminAPI.dashboard.getBusinessSummary().catch(() => null) : null
    ])
    if (request !== summaryRequest) return
    summary.value = result
    if (fullHistory) allDaily.value = fullHistory.daily
    if (!range) allDaily.value = result.daily
    queriedRange.value = range
    detailKind.value = null
    ++detailsRequest
  } catch (error) {
    if (request !== summaryRequest) return
    loadError.value = true
    console.error('读取经营历史失败:', error)
    app.showError(t('admin.businessHistory.loadFailed'))
  } finally {
    if (request === summaryRequest) loading.value = false
  }
}

// 日期查询与全维度余额独立加载，任一读取失败不阻断另一张卡片。
onMounted(() => { void loadSummary(); void refreshBalances() })
</script>
