<template>
  <AppLayout>
    <main class="mx-auto w-full max-w-[1480px] space-y-6 px-4 py-5 sm:px-6">
      <header class="flex flex-col gap-3 border-b border-gray-200 pb-4 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
        <!-- 页面标题已由 AppLayout 外层统一展示，这里只保留余额中心快捷入口。 -->
        <RouterLink to="/admin/accounts" class="btn btn-secondary self-start sm:self-auto">
          <Icon name="server" size="sm" />{{ t('admin.balanceCenter.accountCenter') }}
        </RouterLink>
      </header>

      <nav class="border-b border-gray-200 dark:border-dark-700" role="tablist" :aria-label="t('admin.balanceCenter.title')">
        <div class="flex gap-6">
          <button
            type="button"
            role="tab"
            data-test="tab-add-recharge"
            class="border-b-2 px-1 pb-3 text-sm font-semibold transition-colors"
            :class="activeTab === 'add' ? activeTabClass : inactiveTabClass"
            :aria-selected="activeTab === 'add'"
            @click="switchTab('add')"
          >
            {{ t('admin.balanceCenter.addRecharge') }}
          </button>
          <button
            type="button"
            role="tab"
            data-test="tab-recharge-records"
            class="border-b-2 px-1 pb-3 text-sm font-semibold transition-colors"
            :class="activeTab === 'records' ? activeTabClass : inactiveTabClass"
            :aria-selected="activeTab === 'records'"
            @click="switchTab('records')"
          >
            {{ t('admin.balanceCenter.recordsTitle') }}
          </button>
        </div>
      </nav>

      <section v-if="activeTab === 'add'" class="space-y-3" aria-labelledby="recharge-entry-title">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 id="recharge-entry-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.balanceCenter.addRecharge') }}</h2>
          </div>
          <div class="flex w-full items-end gap-2 sm:w-auto">
            <label class="min-w-0 flex-1 sm:w-64 sm:flex-none">
              <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.balanceCenter.rechargeAt') }}</span>
              <input v-model="rechargeTime" data-test="recharge-time" type="datetime-local" step="1" class="input w-full" />
            </label>
            <button type="button" data-test="recharge-time-now" class="btn btn-secondary h-10 shrink-0" @click="resetRechargeTime">
              {{ t('admin.balanceCenter.backToNow') }}
            </button>
          </div>
        </div>

        <div class="overflow-hidden border-y border-gray-200 dark:border-dark-700">
          <div class="hidden grid-cols-[minmax(160px,1fr)_minmax(180px,1.4fr)_minmax(170px,0.8fr)] gap-4 bg-gray-50 px-3 py-2 text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400 sm:grid">
            <span>{{ t('admin.balanceCenter.siteName') }}</span>
            <span>{{ t('admin.balanceCenter.domain') }}</span>
            <span>{{ t('admin.balanceCenter.amount') }}</span>
          </div>
          <div data-test="recharge-list-scroll" class="max-h-[58vh] overflow-y-auto">
            <div
              v-for="site in sortedSites"
              :key="site.id"
              data-test="site-recharge-row"
              class="grid gap-2 border-t border-gray-100 px-3 py-3 first:border-t-0 dark:border-dark-700 sm:grid-cols-[minmax(160px,1fr)_minmax(180px,1.4fr)_minmax(170px,0.8fr)] sm:items-center sm:gap-4"
            >
              <div class="min-w-0">
                <span class="font-medium text-gray-900 dark:text-gray-100">{{ site.display_name || site.name }}</span>
                <span class="ml-2 text-xs text-gray-400 sm:hidden">{{ site.normalized_domain }}</span>
              </div>
              <span class="hidden truncate text-sm text-gray-500 dark:text-gray-400 sm:block">{{ site.normalized_domain }}</span>
              <div class="flex items-center gap-2">
                <input
                  v-model="amounts[site.id]"
                  :data-test="`recharge-amount-${site.id}`"
                  type="number"
                  min="0.01"
                  step="0.01"
                  inputmode="decimal"
                  class="input min-w-0 flex-1"
                  :placeholder="t('admin.balanceCenter.amountPlaceholder')"
                  @keyup.enter="addRecharge(site)"
                />
                <button type="button" class="btn btn-primary h-10 w-10 shrink-0 p-0" :title="t('admin.balanceCenter.addRecharge')" :disabled="busySiteID === site.id" @click="addRecharge(site)">
                  <Icon name="plus" size="sm" />
                </button>
              </div>
            </div>
            <div v-if="!sitesLoading && sites.length === 0" class="px-3 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.balanceCenter.noSites') }}
            </div>
          </div>
        </div>
      </section>

      <section v-else class="space-y-4" aria-labelledby="recharge-records-title">
        <div class="flex flex-col gap-3 border-b border-gray-200 pb-3 dark:border-dark-700 lg:flex-row lg:items-end lg:justify-between">
          <div class="flex items-end gap-5">
            <div>
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.balanceCenter.totalAmount') }}</span>
              <div data-test="total-amount" class="mt-1 text-2xl font-semibold text-gray-900 dark:text-white">¥{{ money(summary.total_amount) }}</div>
            </div>
            <div class="pb-1 text-sm text-gray-500 dark:text-gray-400">{{ summary.total }} {{ t('admin.balanceCenter.records') }}</div>
          </div>

          <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
            <div class="inline-flex h-9 overflow-hidden rounded border border-gray-300 dark:border-dark-600" role="group" :aria-label="t('admin.balanceCenter.timeRange')">
              <button v-for="preset in rangePresets" :key="preset" type="button" :data-test="`range-${preset}`" class="px-3 text-sm transition-colors" :class="rangePreset === preset ? selectedButtonClass : normalButtonClass" @click="selectRange(preset)">
                {{ t(`admin.balanceCenter.ranges.${preset}`) }}
              </button>
            </div>
            <input v-model="customDate" data-test="custom-date" type="date" class="input h-9 sm:w-40" :title="t('admin.balanceCenter.customDate')" @change="selectCustomDate" />
          </div>
        </div>

        <div class="flex items-center justify-between gap-3">
          <h2 id="recharge-records-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.balanceCenter.recordsTitle') }}</h2>
          <div class="inline-flex h-9 overflow-hidden rounded border border-gray-300 dark:border-dark-600" role="group" :aria-label="t('admin.balanceCenter.dimension')">
            <button data-test="dimension-time" type="button" class="px-3 text-sm" :class="dimension === 'time' ? selectedButtonClass : normalButtonClass" @click="dimension = 'time'">
              <Icon name="clock" size="sm" class="mr-1 inline-block" />{{ t('admin.balanceCenter.byTime') }}
            </button>
            <button data-test="dimension-site" type="button" class="px-3 text-sm" :class="dimension === 'site' ? selectedButtonClass : normalButtonClass" @click="dimension = 'site'">
              <Icon name="server" size="sm" class="mr-1 inline-block" />{{ t('admin.balanceCenter.bySite') }}
            </button>
          </div>
        </div>

        <div v-if="dimension === 'time'" class="overflow-x-auto border-y border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[680px] text-sm">
            <thead class="bg-gray-50 text-left text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr><th class="px-3 py-2">{{ t('admin.balanceCenter.rechargeAt') }}</th><th class="px-3 py-2">{{ t('admin.balanceCenter.siteName') }}</th><th class="px-3 py-2 text-right">{{ t('admin.balanceCenter.amount') }}</th><th class="w-14 px-3 py-2"></th></tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="item in summary.items" :key="item.id" class="text-gray-700 dark:text-gray-200">
                <td class="whitespace-nowrap px-3 py-3">{{ rechargeDate(item) }}</td>
                <td class="px-3 py-3">{{ rechargeSiteName(item) }}</td>
                <td class="px-3 py-3 text-right font-medium">¥{{ money(item.amount) }}</td>
                <td class="px-3 py-2 text-right"><button type="button" class="btn btn-ghost h-8 w-8 p-0 text-red-600" :data-test="`delete-recharge-${item.id}`" :title="t('admin.balanceCenter.delete')" @click="removeRecharge(item)"><Icon name="trash" size="sm" /></button></td>
              </tr>
              <tr v-if="!recordsLoading && summary.items.length === 0"><td colspan="4" class="px-3 py-10 text-center text-gray-500 dark:text-gray-400">{{ t('admin.balanceCenter.noRecords') }}</td></tr>
            </tbody>
          </table>
          <Pagination v-if="summary.total > summary.page_size" :page="summary.page" :page-size="summary.page_size" :total="summary.total" @update:page="changePage" @update:page-size="changePageSize" />
        </div>

        <div v-else class="divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700">
          <div v-for="site in sortedSummarySites" :key="siteKey(site.site_id)" data-test="site-summary-row">
            <button type="button" class="grid w-full grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-4 px-3 py-3 text-left hover:bg-gray-50 dark:hover:bg-dark-800" :data-test="`expand-site-${siteKey(site.site_id)}`" @click="toggleSite(site.site_id)">
              <span class="truncate font-medium text-gray-900 dark:text-gray-100"><Icon :name="expandedSites.has(siteKey(site.site_id)) ? 'chevronDown' : 'chevronRight'" size="sm" class="mr-2 inline-block" />{{ site.site_name }}</span>
              <span class="text-sm text-gray-500 dark:text-gray-400">{{ site.record_count }} {{ t('admin.balanceCenter.records') }}</span>
              <span class="min-w-24 text-right font-semibold text-gray-900 dark:text-white">¥{{ money(site.total_amount) }}</span>
            </button>
            <div v-if="expandedSites.has(siteKey(site.site_id))" class="border-t border-gray-100 bg-gray-50/60 px-3 py-2 dark:border-dark-700 dark:bg-dark-800/50">
              <div v-for="item in site.items" :key="item.id" data-test="site-history-item" class="flex items-center justify-between gap-4 py-2 pl-7 text-sm">
                <span class="text-gray-500 dark:text-gray-400">{{ rechargeDate(item) }}</span>
                <span class="font-medium text-gray-800 dark:text-gray-100">¥{{ money(item.amount) }}</span>
              </div>
            </div>
          </div>
          <div v-if="!recordsLoading && summary.sites.length === 0" class="px-3 py-10 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('admin.balanceCenter.noRecords') }}</div>
        </div>
      </section>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { BalanceCenterListParams, BalanceCenterRechargeEvent, BalanceCenterRechargeSummary, BalanceCenterSite } from '@/api/admin/balanceCenter'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'

type Dimension = 'time' | 'site'
type RangePreset = '24h' | 'today' | 'yesterday' | 'all'
type ActiveTab = 'add' | 'records'

const { t } = useI18n()
const app = useAppStore()
const sites = ref<BalanceCenterSite[]>([])
const amounts = reactive<Record<number, string>>({})
const activeTab = ref<ActiveTab>('add')
const sitesLoading = ref(false)
const recordsLoading = ref(false)
const busySiteID = ref<number>()
const dimension = ref<Dimension>('time')
const rangePreset = ref<RangePreset | 'custom'>('24h')
const customDate = ref('')
const rechargeTime = ref(toLocalSecond(new Date()))
const expandedSites = ref(new Set<string>())
const rangePresets: RangePreset[] = ['24h', 'today', 'yesterday', 'all']
const selectedButtonClass = 'bg-gray-900 text-white dark:bg-gray-100 dark:text-gray-900'
const normalButtonClass = 'bg-white text-gray-600 hover:bg-gray-50 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'
const activeTabClass = 'border-primary-600 text-primary-700 dark:border-primary-400 dark:text-primary-300'
const inactiveTabClass = 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'
const summary = reactive<BalanceCenterRechargeSummary>({ total_amount: 0, items: [], sites: [], total: 0, page: 1, page_size: 20 })

const sortedSites = computed(() => [...sites.value].sort((left, right) => {
  const totalDifference = (right.historical_recharge_total ?? 0) - (left.historical_recharge_total ?? 0)
  return totalDifference || left.normalized_domain.localeCompare(right.normalized_domain)
}))
const sortedSummarySites = computed(() => [...summary.sites].sort((left, right) => {
  const totalDifference = right.total_amount - left.total_amount
  return totalDifference || left.site_name.localeCompare(right.site_name)
}))

function toLocalSecond(value: Date) {
  const pad = (input: number) => String(input).padStart(2, '0')
  return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}T${pad(value.getHours())}:${pad(value.getMinutes())}:${pad(value.getSeconds())}`
}

function resetRechargeTime() {
  rechargeTime.value = toLocalSecond(new Date())
}

function dayRange(value: Date) {
  const start = new Date(value.getFullYear(), value.getMonth(), value.getDate())
  const end = new Date(start)
  end.setDate(end.getDate() + 1)
  end.setMilliseconds(-1)
  return { start_time: start.toISOString(), end_time: end.toISOString() }
}

function currentRange(): Pick<BalanceCenterListParams, 'start_time' | 'end_time'> {
  const now = new Date()
  if (rangePreset.value === 'all') return {}
  if (rangePreset.value === '24h') return { start_time: new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString(), end_time: now.toISOString() }
  if (rangePreset.value === 'today') return dayRange(now)
  if (rangePreset.value === 'yesterday') {
    const yesterday = new Date(now)
    yesterday.setDate(yesterday.getDate() - 1)
    return dayRange(yesterday)
  }
  return customDate.value ? dayRange(new Date(`${customDate.value}T00:00:00`)) : {}
}

function requestParams(): BalanceCenterListParams {
  return { page: summary.page, page_size: summary.page_size, ...currentRange() }
}

async function loadSummary() {
  recordsLoading.value = true
  try {
    Object.assign(summary, await adminAPI.balanceCenter.rechargeSummary(requestParams()))
  } catch (error) {
    notifyError(error)
  } finally {
    recordsLoading.value = false
  }
}

async function initial() {
  sitesLoading.value = true
  try {
    sites.value = await adminAPI.balanceCenter.sites()
  } catch (error) {
    notifyError(error)
  } finally {
    sitesLoading.value = false
  }
}

// 充值记录只在管理员主动打开对应 Tab 时查询，避免进入页面就发起无关请求。
async function switchTab(tab: ActiveTab) {
  if (activeTab.value === tab) return
  activeTab.value = tab
  if (tab === 'records') await loadSummary()
}

async function addRecharge(site: BalanceCenterSite) {
  const amount = Number(amounts[site.id])
  if (!Number.isFinite(amount) || amount <= 0) return
  const occurredAt = new Date(rechargeTime.value)
  if (Number.isNaN(occurredAt.getTime())) return
  busySiteID.value = site.id
  try {
    await adminAPI.balanceCenter.createRechargeEvent({
      source: 'manual',
      source_key: `manual-${site.id}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      site_id: site.id,
      amount,
      currency: 'CNY',
      occurred_at: occurredAt.toISOString(),
      note: '',
      record_type: 'recharge'
    })
    amounts[site.id] = ''
    site.historical_recharge_total = (site.historical_recharge_total ?? 0) + amount
    app.showSuccess(t('admin.balanceCenter.rechargeAdded'))
  } catch (error) {
    notifyError(error)
  } finally {
    busySiteID.value = undefined
  }
}

async function removeRecharge(item: BalanceCenterRechargeEvent) {
  if (!item.id || !window.confirm(t('admin.balanceCenter.deleteConfirm'))) return
  try {
    await adminAPI.balanceCenter.deleteRechargeEvent(item.id)
    await loadSummary()
  } catch (error) {
    notifyError(error)
  }
}

async function selectRange(preset: RangePreset) {
  rangePreset.value = preset
  summary.page = 1
  await loadSummary()
}

async function selectCustomDate() {
  if (!customDate.value) return
  rangePreset.value = 'custom'
  summary.page = 1
  await loadSummary()
}

function changePage(page: number) {
  summary.page = page
  void loadSummary()
}

function changePageSize(pageSize: number) {
  summary.page = 1
  summary.page_size = pageSize
  void loadSummary()
}

function siteKey(siteID?: number) {
  return siteID == null ? 'unassigned' : String(siteID)
}

function toggleSite(siteID?: number) {
  const key = siteKey(siteID)
  const next = new Set(expandedSites.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedSites.value = next
}

function rechargeSiteName(item: BalanceCenterRechargeEvent) {
  if (item.site_id != null) {
    const site = sites.value.find(candidate => candidate.id === item.site_id)
    return site?.display_name || site?.name || t('admin.balanceCenter.unassignedSite')
  }
  return item.site_label || t('admin.balanceCenter.unassignedSite')
}

function money(value?: number) {
  return Number(value ?? 0).toFixed(2)
}

function rechargeDate(item: BalanceCenterRechargeEvent) {
  // 期初累计没有真实逐笔日期，数据库时间仅用于稳定排序，不能作为充值日期展示。
  return item.source === 'legacy_opening' ? t('admin.balanceCenter.unknownRechargeDate') : formatDateTime(item.occurred_at)
}

function notifyError(error: unknown) {
  app.showError(error instanceof Error ? error.message : String((error as { message?: string })?.message || error))
}

onMounted(initial)
</script>
