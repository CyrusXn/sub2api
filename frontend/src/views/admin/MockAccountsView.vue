<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accounts.mock.title') }}
            </h1>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.mock.description') }}
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <label class="relative min-w-[240px]">
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
            <button type="button" class="btn btn-secondary" :title="t('admin.accounts.mock.refresh')" @click="regenerateAccounts">
              <Icon name="refresh" size="sm" />
              <span>{{ t('admin.accounts.mock.refresh') }}</span>
            </button>
          </div>
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
          :default-sort-order="'asc'"
          sort-storage-key="admin-mock-accounts-sort"
          column-width-storage-key="admin-mock-accounts-widths"
          :estimate-row-height="84"
          :overscan="5"
          :virtualize-threshold="50"
        >
          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ value }}</span>
          </template>
          <template #cell-name="{ row }">
            <div class="flex min-w-0 flex-col">
              <span class="max-w-[240px] truncate font-medium text-gray-900 dark:text-white" :title="row.email">
                {{ row.name }}
              </span>
            </div>
          </template>
          <template #cell-platform_type="{ row }">
            <PlatformTypeBadge :platform="row.platform" :type="row.type" />
          </template>
          <template #cell-status="{ value }">
            <span
              class="inline-flex items-center rounded-full px-2 py-1 text-xs font-medium"
              :class="statusClass(value)"
            >
              {{ t(`admin.accounts.status.${value}`) }}
            </span>
          </template>
          <template #cell-capacity="{ row }">
            <div class="min-w-[120px]">
              <div class="mb-1 flex justify-between text-xs text-gray-500 dark:text-gray-400">
                <span>{{ row.capacity }}%</span>
                <span>{{ t('admin.accounts.mock.oauth') }}</span>
              </div>
              <div class="h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
                <div class="h-full rounded-full bg-primary-500" :style="{ width: `${row.capacity}%` }"></div>
              </div>
            </div>
          </template>
          <template #cell-last_used_at="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDate(value) }}</span>
          </template>
          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDate(value) }}</span>
          </template>
          <template #cell-expires_at="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ formatDate(value) }}</span>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <div class="flex flex-wrap items-center justify-between gap-3 text-sm text-gray-600 dark:text-gray-300">
          <span>{{ t('admin.accounts.mock.pageSummary', { page, pages: totalPages }) }}</span>
          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-secondary px-3" :disabled="page <= 1" @click="page--">
              <Icon name="chevronLeft" size="sm" />
              <span>{{ t('admin.accounts.mock.previous') }}</span>
            </button>
            <button type="button" class="btn btn-secondary px-3" :disabled="page >= totalPages" @click="page++">
              <span>{{ t('admin.accounts.mock.next') }}</span>
              <Icon name="chevronRight" size="sm" />
            </button>
          </div>
        </div>
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
import Icon from '@/components/icons/Icon.vue'
import type { AccountPlatform, AccountType } from '@/types'
import type { Column } from '@/components/common/types'

interface MockAccount {
  id: number
  name: string
  email: string
  platform: AccountPlatform
  type: AccountType
  status: 'active' | 'inactive' | 'error'
  capacity: number
  last_used_at: string
  created_at: string
  expires_at: string
}

const { t } = useI18n()
const PAGE_SIZE = 30
const MOCK_ACCOUNT_COUNT = 300
const searchQuery = ref('')
const statusFilter = ref('all')
const page = ref(1)
const accounts = ref<MockAccount[]>([])

const columns: Column[] = [
  { key: 'id', label: t('admin.accounts.columns.id'), sortable: true, width: 84 },
  { key: 'name', label: t('admin.accounts.columns.name'), sortable: true, width: 250 },
  { key: 'platform_type', label: t('admin.accounts.columns.platformType'), sortable: false, width: 150 },
  { key: 'status', label: t('admin.accounts.columns.status'), sortable: true, width: 120 },
  { key: 'capacity', label: t('admin.accounts.columns.capacity'), sortable: true, width: 170 },
  { key: 'last_used_at', label: t('admin.accounts.columns.lastUsed'), sortable: true, width: 170 },
  { key: 'created_at', label: t('admin.accounts.columns.createdAt'), sortable: true, width: 170 },
  { key: 'expires_at', label: t('admin.accounts.columns.expiresAt'), sortable: true, width: 170 }
]

const nameParts = ['chen', 'dream', 'nova', 'cloud', 'pixel', 'river', 'spark', 'lumen', 'orbit', 'mint']
const randomChars = 'abcdefghijklmnopqrstuvwxyz0123456789'

function randomString(length: number): string {
  return Array.from({ length }, () => randomChars[Math.floor(Math.random() * randomChars.length)]).join('')
}

function randomEmail(used: Set<string>): string {
  let email = ''
  do {
    const prefix = `${nameParts[Math.floor(Math.random() * nameParts.length)]}${randomString(3 + Math.floor(Math.random() * 5))}`
    email = `${prefix}@gmail.com`
  } while (used.has(email))
  used.add(email)
  return email
}

function randomDate(daysAgo: number, daysAhead = 0): string {
  const now = Date.now()
  const offset = (Math.random() * (daysAgo + daysAhead) - daysAgo) * 24 * 60 * 60 * 1000
  return new Date(now + offset).toISOString()
}

// 每次生成都重新随机邮箱和状态；名称直接使用邮箱，平台统一模拟 OpenAI OAuth。
function generateAccounts(): MockAccount[] {
  const usedEmails = new Set<string>()
  return Array.from({ length: MOCK_ACCOUNT_COUNT }, (_, index) => {
    const email = randomEmail(usedEmails)
    return {
      id: index + 1,
      name: email,
      email,
      platform: 'openai' as const,
      type: 'oauth' as const,
      status: Math.random() > 0.9 ? 'error' : Math.random() > 0.82 ? 'inactive' : 'active',
      capacity: Math.floor(Math.random() * 95) + 5,
      last_used_at: randomDate(7),
      created_at: randomDate(180),
      expires_at: randomDate(14, 180)
    }
  })
}

function regenerateAccounts() {
  accounts.value = generateAccounts()
  page.value = 1
}

const filteredAccounts = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return accounts.value.filter((account) => {
    const matchesStatus = statusFilter.value === 'all' || account.status === statusFilter.value
    const matchesQuery = !query || `${account.name} ${account.email} ${account.platform}`.toLowerCase().includes(query)
    return matchesStatus && matchesQuery
  })
})

const totalPages = computed(() => Math.max(1, Math.ceil(filteredAccounts.value.length / PAGE_SIZE)))
const pagedAccounts = computed(() => {
  const start = (page.value - 1) * PAGE_SIZE
  return filteredAccounts.value.slice(start, start + PAGE_SIZE)
})

watch([searchQuery, statusFilter], () => {
  regenerateAccounts()
})
watch(totalPages, (value) => {
  if (page.value > value) page.value = value
})

function statusClass(status: MockAccount['status']): string {
  if (status === 'active') return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
  if (status === 'inactive') return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

regenerateAccounts()
</script>
