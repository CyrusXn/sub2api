<template>
  <div>
    <div
      v-if="loading && items.length === 0"
      class="grid gap-5 grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
    >
      <div
        v-for="i in 6"
        :key="i"
        class="p-5 rounded-2xl min-h-[280px] bg-white/70 dark:bg-dark-800/60 border border-gray-200/80 dark:border-dark-700/70 animate-pulse"
      >
        <div class="flex items-start gap-3">
          <div class="w-9 h-9 rounded-xl bg-gray-200 dark:bg-dark-700"></div>
          <div class="flex-1 space-y-2">
            <div class="h-4 w-2/3 rounded bg-gray-200 dark:bg-dark-700"></div>
            <div class="h-3 w-1/2 rounded bg-gray-200 dark:bg-dark-700"></div>
          </div>
          <div class="h-6 w-16 rounded-full bg-gray-200 dark:bg-dark-700"></div>
        </div>
        <div class="mt-5 grid grid-cols-2 gap-2">
          <div class="h-16 rounded-xl bg-gray-100 dark:bg-dark-900/40"></div>
          <div class="h-16 rounded-xl bg-gray-100 dark:bg-dark-900/40"></div>
        </div>
        <div class="mt-6 h-5 w-full rounded bg-gray-100 dark:bg-dark-900/40"></div>
      </div>
    </div>

    <EmptyState
      v-else-if="items.length === 0"
      :title="t('channelStatus.empty.title')"
      :description="t('channelStatus.empty.description')"
    />

    <!-- 按平台折叠展示，组内保留使用频率顺序。 -->
    <div v-else class="space-y-4">
      <details
        v-for="group in groupedItems"
        :key="group.key"
        :open="group.key === 'openai'"
        class="group rounded-2xl border border-gray-200/80 bg-white/40 dark:border-dark-700/70 dark:bg-dark-800/30"
      >
        <summary class="flex cursor-pointer list-none items-center gap-2 rounded-2xl px-5 py-4 select-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 [&::-webkit-details-marker]:hidden">
          <svg class="h-4 w-4 text-gray-500 transition-transform group-open:rotate-90" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
            <path fill-rule="evenodd" d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.17 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z" clip-rule="evenodd" />
          </svg>
          <span class="text-sm font-semibold text-gray-700 dark:text-gray-200">
            {{ t(`channelStatus.platformGroups.${group.key}`) }}
          </span>
          <span
            class="px-2 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300"
          >
            {{ group.items.length }}
          </span>
        </summary>
        <div class="grid gap-5 grid-cols-1 px-5 pb-5 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
          <MonitorCard
            v-for="item in group.items"
            :key="item.id"
            :item="item"
            :window="window"
            :availability-value="resolveAvailability(item)"
            :countdown-seconds="countdownSeconds"
            @click="emit('cardClick', item)"
          />
        </div>
      </details>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserMonitorView, UserMonitorDetail } from '@/api/channelMonitor'
import EmptyState from '@/components/common/EmptyState.vue'
import {
  MONITOR_GROUP_ORDER,
  providerGroupOf,
  type MonitorGroupKey,
} from '@/constants/channelMonitor'
import MonitorCard from './MonitorCard.vue'

const props = defineProps<{
  items: UserMonitorView[]
  window: '7d' | '15d' | '30d'
  countdownSeconds: number
  loading: boolean
  detailCache: Record<number, UserMonitorDetail>
}>()

const emit = defineEmits<{
  (e: 'cardClick', item: UserMonitorView): void
}>()

const { t } = useI18n()

/**
 * 按平台分组后的渠道列表。
 *
 * 分组顺序固定（MONITOR_GROUP_ORDER），组内保持后端返回的原有顺序；
 * 空分组直接过滤掉，避免出现只有标题没有卡片的空段。
 */
const groupedItems = computed<{ key: MonitorGroupKey; items: UserMonitorView[] }[]>(() => {
  const buckets = new Map<MonitorGroupKey, UserMonitorView[]>()
  for (const item of props.items) {
    const key = providerGroupOf(item)
    const bucket = buckets.get(key)
    if (bucket) {
      bucket.push(item)
    } else {
      buckets.set(key, [item])
    }
  }
  return MONITOR_GROUP_ORDER
    .filter(key => (buckets.get(key)?.length ?? 0) > 0)
    .map(key => ({ key, items: buckets.get(key) as UserMonitorView[] }))
})

function resolveAvailability(item: UserMonitorView): number | null {
  if (props.window === '7d') {
    return item.availability_7d ?? null
  }
  if (item.passive) {
    return props.window === '15d' ? item.availability_15d ?? null : item.availability_30d ?? null
  }
  const detail = props.detailCache[item.id]
  if (!detail) return null
  const primary = detail.models.find(m => m.model === item.primary_model)
  if (!primary) return null
  return props.window === '15d' ? primary.availability_15d ?? null : primary.availability_30d ?? null
}
</script>
