<template>
  <aside
    v-if="reminders.length"
    class="fixed bottom-4 right-4 z-[70] w-[min(28rem,calc(100vw-2rem))] overflow-hidden rounded-2xl border border-amber-300 bg-white shadow-2xl dark:border-amber-700 dark:bg-dark-800"
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
        <span class="shrink-0 text-lg" aria-hidden="true">⚠</span>
        <span class="truncate">{{ t('admin.balanceCenter.reminder.title') }}</span>
      </span>
      <span class="shrink-0 rounded-full bg-amber-600 px-2.5 py-1 text-xs font-bold text-white">{{ reminders.length }}</span>
      <span class="sr-only">{{ collapsed ? t('admin.balanceCenter.reminder.expand') : t('admin.balanceCenter.reminder.collapse') }}</span>
    </button>

    <div v-if="!collapsed" id="upstream-balance-reminder-list" class="max-h-[60vh] space-y-3 overflow-y-auto p-3">
      <article
        v-for="reminder in reminders"
        :key="reminder.host"
        class="rounded-xl border border-amber-200 bg-amber-50/60 p-3 text-sm dark:border-amber-800 dark:bg-dark-900"
        :data-testid="`upstream-balance-reminder-${reminder.host}`"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="truncate font-semibold text-gray-900 dark:text-gray-100">{{ reminder.display_name || reminder.host }}</p>
            <p class="truncate text-xs text-gray-500">{{ reminder.host }}</p>
          </div>
          <span class="shrink-0 rounded-md bg-amber-200 px-2 py-1 text-xs font-semibold text-amber-950 dark:bg-amber-900 dark:text-amber-100">
            {{ t('admin.balanceCenter.reminder.increased') }}
          </span>
        </div>
        <p class="mt-2 font-medium text-gray-800 dark:text-gray-200">
          {{ formatAmount(reminder.old_balance, reminder.currency) }} → {{ formatAmount(reminder.new_balance, reminder.currency) }}
          <span class="text-emerald-700 dark:text-emerald-400">(+{{ formatAmount(reminder.delta, reminder.currency) }})</span>
        </p>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.balanceCenter.reminder.recordRecharge') }}</p>
        <time class="mt-1 block text-xs text-gray-400" :datetime="reminder.detected_at">{{ formatDate(reminder.detected_at) }}</time>
      </article>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  subscribeUpstreamSiteBalanceReminders,
  type UpstreamSiteBalanceReminder,
  type UpstreamSiteBalanceReminderEvent
} from '@/api/admin/balanceCenter'

const { t, locale } = useI18n()
const reminders = ref<UpstreamSiteBalanceReminder[]>([])
const collapsed = ref(false)
const latestVersions = new Map<string, number>()
let unsubscribe: (() => void) | null = null

function applyEvent(event: UpstreamSiteBalanceReminderEvent) {
  if (event.type === 'snapshot') {
    reminders.value = event.reminders ?? []
    latestVersions.clear()
    for (const [host, version] of Object.entries(event.versions ?? {})) latestVersions.set(host, version)
    for (const reminder of reminders.value) {
      latestVersions.set(reminder.host, Math.max(latestVersions.get(reminder.host) ?? -1, reminder.version))
    }
    reminders.value = reminders.value.filter((reminder) => reminder.version >= (latestVersions.get(reminder.host) ?? reminder.version))
    return
  }
  if (event.type === 'resolved' && event.host) {
    const latestVersion = latestVersions.get(event.host) ?? -1
    if (event.version !== undefined && event.version < latestVersion) return
    latestVersions.set(event.host, event.version ?? latestVersion + 1)
    reminders.value = reminders.value.filter((item) => item.host !== event.host || (event.version ?? Number.MAX_SAFE_INTEGER) < item.version)
    return
  }
  if (event.type === 'upsert' && event.reminder) {
    const latestVersion = latestVersions.get(event.reminder.host) ?? -1
    if (event.reminder.version < latestVersion) return
    latestVersions.set(event.reminder.host, event.reminder.version)
    const index = reminders.value.findIndex((item) => item.host === event.reminder?.host)
    if (index < 0) reminders.value = [...reminders.value, event.reminder]
    else reminders.value = reminders.value.map((item, i) => i === index ? event.reminder! : item)
  }
}

function formatAmount(value: number, currency: string) {
  return `${new Intl.NumberFormat(locale.value, { maximumFractionDigits: 2 }).format(value)} ${currency}`
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale.value)
}

onMounted(() => {
  unsubscribe = subscribeUpstreamSiteBalanceReminders(applyEvent)
})

onBeforeUnmount(() => {
  unsubscribe?.()
  unsubscribe = null
})
</script>
