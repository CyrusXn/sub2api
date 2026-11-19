<template>
  <aside
    v-if="items.length"
    class="fixed bottom-4 right-4 z-[70] w-[min(30rem,calc(100vw-2rem))] overflow-hidden rounded-lg border border-amber-300 bg-white shadow-2xl dark:border-amber-700 dark:bg-dark-800"
    data-testid="upstream-balance-reminders"
    aria-live="polite"
  >
    <button
      type="button"
      class="flex w-full items-center justify-between gap-3 bg-amber-50 px-4 py-3 text-left dark:bg-amber-950/40"
      :aria-expanded="!collapsed"
      aria-controls="upstream-balance-reminder-list"
      @click="collapsed = !collapsed"
    >
      <span class="flex min-w-0 items-center gap-2 font-semibold text-amber-950 dark:text-amber-100">
        <span aria-hidden="true">!</span>
        <span class="truncate">{{ t('admin.balanceCenter.reminder.title') }}</span>
      </span>
      <span class="shrink-0 rounded-full bg-amber-600 px-2.5 py-1 text-xs font-bold text-white">{{ items.length }}</span>
      <span class="sr-only">{{ collapsed ? t('admin.balanceCenter.reminder.expand') : t('admin.balanceCenter.reminder.collapse') }}</span>
    </button>

    <div v-if="!collapsed" id="upstream-balance-reminder-list" class="max-h-[65vh] space-y-3 overflow-y-auto p-3">
      <article
        v-for="item in items"
        :key="item.id"
        class="space-y-2 border-b border-gray-200 pb-3 last:border-0 last:pb-0 dark:border-dark-600"
        :data-testid="`recharge-review-${item.id}`"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ item.site_label || item.source_key }}</p>
            <time class="block text-xs text-gray-500" :datetime="item.occurred_at">{{ formatDate(item.occurred_at) }}</time>
          </div>
          <span class="shrink-0 text-xs text-gray-500">{{ item.currency }}</span>
        </div>
        <div class="flex items-end gap-2">
          <label class="min-w-0 flex-1 text-xs text-gray-600 dark:text-gray-300">
            {{ t('admin.balanceCenter.reminder.amount') }}
            <input
              v-model="amounts[item.id]"
              type="number"
              min="0.01"
              step="0.01"
              class="mt-1 w-full rounded border border-gray-300 bg-white px-2 py-1.5 text-sm text-gray-900 dark:border-dark-500 dark:bg-dark-900 dark:text-gray-100"
              :aria-label="`${t('admin.balanceCenter.reminder.amount')} ${item.site_label || item.source_key}`"
            />
          </label>
          <button type="button" class="rounded border border-gray-300 px-2 py-1.5 text-xs disabled:opacity-50 dark:border-dark-500" :disabled="busyId === item.id" @click="saveAmount(item)">
            {{ t('admin.balanceCenter.reminder.save') }}
          </button>
          <button type="button" class="rounded bg-emerald-700 px-2.5 py-1.5 text-xs font-semibold text-white disabled:opacity-50" :disabled="busyId === item.id" @click="confirm(item)">
            {{ t('admin.balanceCenter.reminder.confirm') }}
          </button>
        </div>
      </article>
      <div class="flex justify-end border-t border-gray-200 pt-3 dark:border-dark-600">
        <button type="button" class="rounded bg-emerald-700 px-3 py-2 text-xs font-semibold text-white disabled:opacity-50" :disabled="busyId !== null" @click="confirmAll">
          {{ t('admin.balanceCenter.reminder.confirmAll') }}
        </button>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  balanceCenterAPI,
  subscribeUpstreamSiteBalanceReminders,
  type BalanceCenterRechargeEvent,
  type UpstreamSiteBalanceReminderEvent
} from '@/api/admin/balanceCenter'
import { useAppStore } from '@/stores/app'

const { t, locale } = useI18n()
const app = useAppStore()
const items = ref<BalanceCenterRechargeEvent[]>([])
const amounts = ref<Record<number, string>>({})
const collapsed = ref(true)
const busyId = ref<number | null>(null)
let unsubscribe: (() => void) | null = null
let requestId = 0

async function loadReviews() {
  const request = ++requestId
  try {
    const result = await balanceCenterAPI.pendingRechargeReviews()
    if (request !== requestId) return
    items.value = result.items
    amounts.value = Object.fromEntries(result.items.map(item => [item.id!, String(item.amount)]))
  } catch {
    app.showError(t('admin.balanceCenter.reminder.loadError'))
  }
}

function onReminderEvent(event: UpstreamSiteBalanceReminderEvent) {
  if (event.type === 'upsert') void loadReviews()
}

async function saveAmount(item: BalanceCenterRechargeEvent) {
  const amount = Number(amounts.value[item.id!])
  if (!Number.isFinite(amount) || amount <= 0) return
  busyId.value = item.id!
  try {
    await balanceCenterAPI.updatePendingRechargeAmount(item.id!, amount)
    item.amount = amount
    app.showSuccess(t('admin.balanceCenter.reminder.saved'))
  } catch (error) {
    app.showError(error instanceof Error ? error.message : t('admin.balanceCenter.reminder.loadError'))
  } finally {
    busyId.value = null
  }
}

async function confirm(item: BalanceCenterRechargeEvent) {
  busyId.value = item.id!
  try {
		const amount = Number(amounts.value[item.id!])
		if (!Number.isFinite(amount) || amount <= 0) return
		if (amount !== item.amount) {
			await balanceCenterAPI.updatePendingRechargeAmount(item.id!, amount)
			item.amount = amount
		}
    await balanceCenterAPI.confirmPendingRecharge(item.id!)
    items.value = items.value.filter(row => row.id !== item.id)
    app.showSuccess(t('admin.balanceCenter.reminder.confirmed'))
  } catch (error) {
    app.showError(error instanceof Error ? error.message : t('admin.balanceCenter.reminder.loadError'))
  } finally {
    busyId.value = null
  }
}

async function confirmAll() {
  busyId.value = -1
  try {
    await balanceCenterAPI.confirmAllPendingRecharges()
    items.value = []
    app.showSuccess(t('admin.balanceCenter.reminder.confirmedAll'))
  } catch (error) {
    app.showError(error instanceof Error ? error.message : t('admin.balanceCenter.reminder.loadError'))
  } finally {
    busyId.value = null
  }
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale.value)
}

onMounted(() => {
  void loadReviews()
  unsubscribe = subscribeUpstreamSiteBalanceReminders(onReminderEvent)
})

onBeforeUnmount(() => {
  ++requestId
  unsubscribe?.()
  unsubscribe = null
})
</script>
