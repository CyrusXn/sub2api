<template>
  <aside
    v-if="items.length"
    class="fixed z-[70]"
    :class="{ 'w-[min(30rem,calc(100vw-2rem))] overflow-hidden rounded-lg border border-amber-300 bg-white shadow-2xl dark:border-amber-700 dark:bg-dark-800': !collapsed }"
    :style="reminderStyle"
    data-testid="upstream-balance-reminders"
    aria-live="polite"
  >
    <button
      type="button"
      class="flex items-center"
      :class="collapsed ? 'h-8 w-8 touch-none select-none justify-center rounded-full text-xs font-bold tabular-nums text-white focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-amber-500' : 'w-full justify-between gap-3 bg-amber-50 px-4 py-3 text-left dark:bg-amber-950/40'"
      :style="collapsed ? { cursor: drag ? 'grabbing' : 'grab' } : undefined"
      :aria-label="collapsed ? t('admin.balanceCenter.reminder.expand') : t('admin.balanceCenter.reminder.collapse')"
      :aria-expanded="!collapsed"
      :aria-controls="collapsed ? undefined : 'upstream-balance-reminder-list'"
      @pointerdown="startDrag"
      @pointermove="moveDrag"
      @pointerup="finishDrag($event, true)"
      @pointercancel="finishDrag($event, false)"
      @lostpointercapture="finishDrag($event, false)"
      @click="toggleReminder"
    >
      <span
        v-if="collapsed"
        class="pointer-events-none relative grid h-full w-full place-items-center"
        :class="{ 'review-dot-held': drag }"
        data-testid="collapsed-review-dot"
        aria-hidden="true"
      >
        <span class="review-dot-orbit"></span>
        <span class="review-dot-face relative grid h-full w-full place-items-center rounded-full bg-amber-600">
          {{ total > 99 ? '99+' : total }}
        </span>
      </span>
      <template v-else>
        <span class="flex min-w-0 items-center gap-2 font-semibold text-amber-950 dark:text-amber-100">
          <span aria-hidden="true">!</span>
          <span class="truncate">{{ t('admin.balanceCenter.reminder.title') }}</span>
        </span>
        <span class="shrink-0 rounded-full bg-amber-600 px-2.5 py-1 text-xs font-bold text-white">{{ total }}</span>
      </template>
    </button>

    <div v-if="!collapsed" id="upstream-balance-reminder-list" class="max-h-[min(65vh,calc(100dvh-5rem))] space-y-3 overflow-y-auto p-3">
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
              :disabled="busyId !== null"
              class="mt-1 w-full rounded border border-gray-300 bg-white px-2 py-1.5 text-sm text-gray-900 dark:border-dark-500 dark:bg-dark-900 dark:text-gray-100"
              :aria-label="`${t('admin.balanceCenter.reminder.amount')} ${item.site_label || item.source_key}`"
            />
          </label>
          <button type="button" class="rounded bg-emerald-700 px-2.5 py-1.5 text-xs font-semibold text-white disabled:opacity-50" :disabled="busyId !== null" @click="confirm(item)">
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
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  balanceCenterAPI,
  subscribeUpstreamSiteBalanceReminders,
  type BalanceCenterRechargeEvent,
  type RechargeReviewPosition,
  type UpstreamSiteBalanceReminderEvent
} from '@/api/admin/balanceCenter'
import { useAppStore } from '@/stores/app'

const { t, locale } = useI18n()
const app = useAppStore()
const items = ref<BalanceCenterRechargeEvent[]>([])
const total = ref(0)
const amounts = ref<Record<number, string | number>>({})
const collapsed = ref(true)
const busyId = ref<number | null>(null)
let unsubscribe: (() => void) | null = null
let requestId = 0

const inset = 16
const badgeSize = 32
const viewport = shallowRef({ width: window.innerWidth, height: window.innerHeight })
const position = shallowRef<RechargeReviewPosition | null>(null)
const drag = shallowRef<{ id: number; clientX: number; clientY: number; x: number; y: number } | null>(null)
const draggedPosition = shallowRef<RechargeReviewPosition | null>(null)
let positionReady = false
let savingPosition = false
let suppressClick = false
let mounted = false

const movement = computed(() => ({
  x: Math.max(0, viewport.value.width - inset * 2 - badgeSize),
  y: Math.max(0, viewport.value.height - inset * 2 - badgeSize)
}))
const badgePosition = computed(() => {
  const saved = draggedPosition.value ?? position.value
  return {
    x: inset + (saved?.x ?? 1) * movement.value.x,
    y: inset + (saved ? saved.y * movement.value.y : Math.min(64, movement.value.y))
  }
})
const reminderStyle = computed(() => {
  const { x, y } = badgePosition.value
  if (collapsed.value) return { left: `${x}px`, top: `${y}px` }
  // 面板按徽标位置展开，靠近屏幕边缘时向内收，避免审核按钮出界。
  const panelWidth = `min(30rem, calc(100vw - ${inset * 2}px))`
  return {
    left: `clamp(${inset}px, ${x + badgeSize}px - ${panelWidth}, 100vw - ${inset}px - ${panelWidth})`,
    top: `clamp(${inset}px, ${y}px, 100dvh - min(65vh, 100dvh - 5rem) - 4rem)`
  }
})

function updateViewport() {
  viewport.value = { width: window.innerWidth, height: window.innerHeight }
}

function startDrag(event: PointerEvent) {
  if (!collapsed.value || drag.value || event.button !== 0) return
  suppressClick = false
  if (!positionReady || savingPosition) return
  const { x, y } = badgePosition.value
  drag.value = { id: event.pointerId, clientX: event.clientX, clientY: event.clientY, x, y }
  const button = event.currentTarget as HTMLButtonElement
  button.setPointerCapture?.(event.pointerId)
}

function moveDrag(event: PointerEvent) {
  const start = drag.value
  if (!start || start.id !== event.pointerId) return
  const dx = event.clientX - start.clientX
  const dy = event.clientY - start.clientY
  if (!draggedPosition.value && Math.hypot(dx, dy) < 5) return
  const clamp = (value: number, max: number) => max > 0 ? Math.max(0, Math.min(1, value / max)) : 0
  draggedPosition.value = {
    x: clamp(start.x + dx - inset, movement.value.x),
    y: clamp(start.y + dy - inset, movement.value.y)
  }
}

function finishDrag(event: PointerEvent, save: boolean) {
  if (drag.value?.id !== event.pointerId) return
  const next = draggedPosition.value
  drag.value = null
  draggedPosition.value = null
  suppressClick = next !== null
  if (save && next) void persistPosition(next)
}

function toggleReminder(event: MouseEvent) {
  if (suppressClick && event.detail !== 0) {
    suppressClick = false
    return
  }
  collapsed.value = !collapsed.value
}

async function loadPosition() {
  try {
    const saved = await balanceCenterAPI.rechargeReviewPosition()
    if (mounted) position.value = saved
  } catch {
    // 暂时使用默认位置，位置读取失败不影响审核。
  } finally {
    positionReady = true
  }
}

async function persistPosition(next: RechargeReviewPosition) {
  const previous = position.value
  position.value = next
  savingPosition = true
  try {
    await balanceCenterAPI.saveRechargeReviewPosition(next)
  } catch {
    position.value = previous
    if (mounted) app.showError(t('admin.balanceCenter.reminder.positionSaveError'))
  } finally {
    savingPosition = false
  }
}

async function loadReviews() {
  const request = ++requestId
  try {
    const result = await balanceCenterAPI.pendingRechargeReviews()
    if (request !== requestId) return
    // 实时刷新仅更新未编辑的金额，不覆盖正在输入的修改。
    const previous = new Map(items.value.map(item => [item.id, item.amount]))
    amounts.value = Object.fromEntries(result.items.map(item => {
      const draft = amounts.value[item.id]
      return [item.id, draft !== undefined && Number(draft) !== previous.get(item.id) ? draft : item.amount]
    }))
    items.value = result.items
    total.value = result.total
    if (!result.items.length) collapsed.value = true
  } catch {
    app.showError(t('admin.balanceCenter.reminder.loadError'))
  }
}

function onReminderEvent(event: UpstreamSiteBalanceReminderEvent) {
  if (event.type === 'upsert') void loadReviews()
}

function reviewAmount(item: BalanceCenterRechargeEvent) {
  const amount = Number(amounts.value[item.id])
  if (Number.isFinite(amount) && amount > 0) return amount
  app.showError(t('admin.balanceCenter.reminder.invalidAmount'))
}

async function confirm(item: BalanceCenterRechargeEvent) {
  if (busyId.value !== null) return
  const amount = reviewAmount(item)
  if (amount === undefined) return
  busyId.value = item.id
  try {
    await balanceCenterAPI.confirmPendingRecharge(item.id, amount === item.amount ? undefined : amount)
    ++requestId
    items.value = items.value.filter(row => row.id !== item.id)
    delete amounts.value[item.id]
    total.value = Math.max(0, total.value - 1)
    if (!items.value.length) collapsed.value = true
    if (total.value > items.value.length) void loadReviews()
    app.showSuccess(t('admin.balanceCenter.reminder.confirmed'))
  } catch (error) {
    app.showError(error instanceof Error ? error.message : t('admin.balanceCenter.reminder.loadError'))
  } finally {
    busyId.value = null
  }
}

async function confirmAll() {
  if (busyId.value !== null || !items.value.length) return
  const edits: { id: number; amount: number }[] = []
  for (const item of items.value) {
    const amount = reviewAmount(item)
    if (amount === undefined) return
    if (amount !== item.amount) edits.push({ id: item.id, amount })
  }
  busyId.value = -1
  try {
    await balanceCenterAPI.confirmAllPendingRecharges(edits)
    ++requestId
    items.value = []
    amounts.value = {}
    total.value = 0
    collapsed.value = true
    app.showSuccess(t('admin.balanceCenter.reminder.confirmedAll'))
    // 确认期间新到达的待审记录仍应重新显示。
    await loadReviews()
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
  mounted = true
  window.addEventListener('resize', updateViewport)
  void loadPosition()
  void loadReviews()
  unsubscribe = subscribeUpstreamSiteBalanceReminders(onReminderEvent)
})

onBeforeUnmount(() => {
  mounted = false
  window.removeEventListener('resize', updateViewport)
  ++requestId
  unsubscribe?.()
  unsubscribe = null
})
</script>

<style scoped>
/* 动效只作用于视觉层，保持按钮的拖拽坐标和点击区域稳定。 */
.review-dot-face {
  box-shadow: 0 0 9px rgb(245 158 11 / 65%);
  animation: review-dot-breathe 1.8s ease-in-out infinite;
}

.review-dot-orbit {
  position: absolute;
  inset: -7px;
  border: 1px solid rgb(245 158 11 / 28%);
  border-block-color: rgb(245 158 11 / 85%);
  border-radius: 50%;
  animation: review-dot-spin 3.2s linear infinite;
}

.review-dot-orbit::before,
.review-dot-orbit::after {
  position: absolute;
  left: 50%;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: #fbbf24;
  box-shadow: 0 0 6px 1px #f59e0b;
  transform: translateX(-50%);
  content: '';
}

.review-dot-orbit::before {
  top: -2px;
}

.review-dot-orbit::after {
  bottom: -2px;
  opacity: 0.7;
}

.review-dot-held .review-dot-face,
.review-dot-held .review-dot-orbit {
  animation-play-state: paused;
}

@keyframes review-dot-breathe {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.2); }
}

@keyframes review-dot-spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .review-dot-face,
  .review-dot-orbit {
    animation: none;
  }
}
</style>
