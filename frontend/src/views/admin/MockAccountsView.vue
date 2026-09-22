<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex min-w-0 flex-wrap items-center gap-3">
            <label class="relative w-full sm:w-[260px]">
              <span class="sr-only">{{ t('admin.accounts.mock.search') }}</span>
              <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                v-model="searchQuery"
                type="search"
                class="input pl-9"
                :placeholder="t('admin.accounts.mock.search')"
              />
            </label>
            <select v-model="statusFilter" class="input w-auto min-w-[130px]">
              <option value="all">{{ t('admin.accounts.mock.allStatuses') }}</option>
              <option value="active">{{ t('admin.accounts.status.active') }}</option>
              <option value="inactive">{{ t('admin.accounts.status.inactive') }}</option>
              <option value="error">{{ t('admin.accounts.status.error') }}</option>
            </select>
            <select v-model="planFilter" class="input w-auto min-w-[160px]" :aria-label="t('admin.accounts.mock.allPlans')">
              <option value="all">{{ t('admin.accounts.mock.allPlans') }}</option>
              <option v-for="planType in planTypes" :key="planType" :value="planType">{{ openAIPlanTypeLabel(planType) }}</option>
            </select>
          </div>
          <button type="button" class="btn btn-secondary" :title="t('admin.accounts.mock.refresh')" @click="regenerateAccounts">
            <Icon name="refresh" size="sm" />
            <span>{{ t('admin.accounts.mock.refresh') }}</span>
          </button>
        </div>
        <div class="mt-3 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
          <span class="inline-flex h-2 w-2 rounded-full bg-emerald-500"></span>
          <span>{{ t('admin.accounts.mock.resultCount', { count: filteredAccounts.length }) }}</span>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="pagedAccounts"
          :loading="false"
          row-key="id"
          :default-sort-key="'id'"
          :default-sort-order="'desc'"
          :server-side-sort="true"
          column-width-storage-key="admin-mock-accounts-widths-v2"
          :estimate-row-height="112"
          :overscan="5"
          :virtualize-threshold="50"
          @sort="handleSort"
        >
          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ value }}</span>
          </template>
          <template #cell-name="{ row }">
            <div class="flex min-w-0 flex-col">
              <span class="max-w-full truncate font-medium text-gray-900 dark:text-white" :title="row.name">
                {{ row.name }}
              </span>
              <span class="max-w-full truncate text-xs text-gray-500 dark:text-gray-400" :title="row.credentials.email">{{ row.credentials.email }}</span>
            </div>
          </template>
          <template #cell-platform_type="{ row }">
            <div class="flex flex-col gap-1 py-2">
              <PlatformTypeBadge :platform="row.platform" :type="row.type" :plan-type="row.credentials.plan_type" :privacy-mode="row.extra.privacy_mode" :subscription-expires-at="row.credentials.subscription_expires_at" />
              <span class="inline-flex items-center gap-1.5 pl-0.5 text-[11px] leading-4 text-gray-500 dark:text-gray-400">
                <span class="h-1.5 w-1.5 rounded-full bg-gray-300 dark:bg-dark-500" />
                {{ t('admin.accounts.openai.compactAuto') }}
              </span>
            </div>
          </template>
          <template #cell-status="{ row }">
            <AccountStatusIndicator :account="row" />
          </template>
          <template #cell-current_concurrency="{ row }">
            <AccountCapacityCell :account="row" />
          </template>
          <template #cell-schedulable="{ row }">
            <button type="button" role="switch" :aria-checked="row.schedulable" :aria-label="t('admin.accounts.schedulable')" :title="t(row.schedulable ? 'admin.accounts.schedulableEnabled' : 'admin.accounts.schedulableDisabled')" class="relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500" :class="row.schedulable ? 'bg-primary-500' : 'bg-gray-200 dark:bg-dark-600'" @click="row.schedulable = !row.schedulable">
              <span class="pointer-events-none h-4 w-4 rounded-full bg-white shadow transition-transform" :class="row.schedulable ? 'translate-x-4' : 'translate-x-0'" />
            </button>
          </template>
          <template #cell-usage="{ row }">
            <div class="flex flex-col gap-1.5">
              <UsageProgressBar label="5h" :utilization="row.usage.fiveHour" :resets-at="row.usage.fiveHourReset" color="indigo" />
              <UsageProgressBar label="7d" :utilization="row.usage.sevenDay" :resets-at="row.usage.sevenDayReset" color="emerald" />
            </div>
          </template>
          <template #cell-rate_multiplier="{ value }">
            <span class="font-mono text-sm text-gray-600 dark:text-gray-300">{{ value.toFixed(2) }}x</span>
          </template>
          <template #cell-groups><span class="text-gray-400">-</span></template>
          <template #cell-upstream_balance><span class="text-gray-400">-</span></template>
          <template #cell-last_used_at="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDate(value) }}</span>
          </template>
          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDate(value) }}</span>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination v-model:page="page" v-model:page-size="pageSize" :total="filteredAccounts.length" />
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Pagination from '@/components/common/Pagination.vue'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'
import Icon from '@/components/icons/Icon.vue'
import { generateMockAccounts } from '@/utils/mockAccounts'
import { openAIPlanTypeLabel } from '@/utils/planType'
import type { Column } from '@/components/common/types'

const { t } = useI18n()
const pageSize = ref(50)
const searchQuery = ref('')
const statusFilter = ref('all')
const planFilter = ref('all')
const planTypes = ['self_serve_business_prolite', 'team', 'pro', 'plus']
const page = ref(1)
const accounts = ref(generateMockAccounts())
const sortKey = ref('id')
const sortOrder = ref<'asc' | 'desc'>('desc')

const columns: Column[] = [
  { key: 'name', label: t('admin.accounts.columns.name'), sortable: true, width: 290 },
  { key: 'id', label: t('admin.accounts.columns.id'), sortable: true, width: 100 },
  { key: 'platform_type', label: t('admin.accounts.columns.platformType'), sortable: false, width: 230 },
  { key: 'current_concurrency', label: t('admin.accounts.columns.capacity'), sortable: true, width: 110 },
  { key: 'status', label: t('admin.accounts.columns.status'), sortable: true, width: 90 },
  { key: 'schedulable', label: t('admin.accounts.columns.schedulable'), sortable: true, width: 90 },
  { key: 'usage', label: t('admin.accounts.columns.usageWindows'), sortable: false, width: 210 },
  { key: 'upstream_balance', label: t('admin.accounts.columns.upstreamBalance'), sortable: false, width: 140 },
  { key: 'rate_multiplier', label: t('admin.accounts.columns.billingRateMultiplier'), sortable: true, width: 110 },
  { key: 'groups', label: t('admin.accounts.columns.groups'), sortable: false, width: 90 },
  { key: 'last_used_at', label: t('admin.accounts.columns.lastUsed'), sortable: true, width: 170 },
  { key: 'created_at', label: t('admin.accounts.columns.createdAt'), sortable: true, width: 170 }
]

function regenerateAccounts() {
  accounts.value = generateMockAccounts()
  page.value = 1
}

const filteredAccounts = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return accounts.value.filter((account) => {
    const matchesStatus = statusFilter.value === 'all' || account.status === statusFilter.value
    const matchesPlan = planFilter.value === 'all' || account.credentials.plan_type === planFilter.value
    const matchesQuery = !query || `${account.id} ${account.name} ${account.platform} ${openAIPlanTypeLabel(account.credentials.plan_type)}`.toLowerCase().includes(query)
    return matchesStatus && matchesPlan && matchesQuery
  })
})

const sortedAccounts = computed(() => [...filteredAccounts.value].sort((a, b) => {
  const key = sortKey.value as keyof typeof a
  const left = a[key]
  const right = b[key]
  const result = typeof left === 'number' && typeof right === 'number'
    ? left - right
    : String(left ?? '').localeCompare(String(right ?? ''))
  return sortOrder.value === 'asc' ? result : -result
}))
const totalPages = computed(() => Math.max(1, Math.ceil(filteredAccounts.value.length / pageSize.value)))
const pagedAccounts = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return sortedAccounts.value.slice(start, start + pageSize.value)
})

function handleSort(key: string, order: 'asc' | 'desc') {
  sortKey.value = key
  sortOrder.value = order
  page.value = 1
}

watch([searchQuery, statusFilter, planFilter, pageSize], () => {
  page.value = 1
})
watch(totalPages, (value) => {
  if (page.value > value) page.value = value
})

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

</script>
