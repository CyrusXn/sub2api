<template>
  <Teleport to="body">
    <div v-if="authStore.isAdmin && items.length" class="fixed z-40" :style="{ left: `${position.x}px`, top: `${position.y}px` }">
      <div v-if="expanded" class="fixed inset-0 -z-10" @click="expanded = false" aria-hidden="true"></div>
      <nav v-if="expanded" aria-label="常用功能" class="fixed overflow-y-auto rounded-2xl border border-gray-200 bg-white/95 p-2 shadow-xl backdrop-blur dark:border-dark-600 dark:bg-dark-800/95" :style="panelStyle">
        <template v-for="(item, index) in items" :key="index">
          <RouterLink v-if="item.url.startsWith('/')" :to="item.url" class="flex items-center gap-3 rounded-xl px-4 py-3 text-sm text-gray-800 hover:bg-gray-100 dark:text-gray-100 dark:hover:bg-dark-700" @click="expanded = false"><component :is="adminMenuIcon(item.url)" v-if="adminMenuIcon(item.url)" class="h-5 w-5 shrink-0 text-primary-500" /><Icon v-else name="link" size="md" class="shrink-0 text-primary-500" />{{ item.name }}</RouterLink>
          <a v-else :href="item.url" target="_blank" rel="noopener noreferrer" class="flex items-center gap-3 rounded-xl px-4 py-3 text-sm text-gray-800 hover:bg-gray-100 dark:text-gray-100 dark:hover:bg-dark-700" @click="expanded = false"><component :is="adminMenuIcon(item.url)" v-if="adminMenuIcon(item.url)" class="h-5 w-5 shrink-0 text-primary-500" /><Icon v-else name="link" size="md" class="shrink-0 text-primary-500" />{{ item.name }}</a>
        </template>
      </nav>
      <button type="button" aria-label="常用功能（按住拖动）" :aria-expanded="expanded" class="flex h-14 w-14 touch-none select-none items-center justify-center rounded-2xl border border-white/30 bg-gray-700/70 shadow-lg backdrop-blur focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" @pointerdown="startDrag" @pointermove="moveDrag" @pointerup="endDrag" @pointercancel="cancelDrag" @lostpointercapture="cancelDrag" @click="toggle" @keydown.esc="expanded = false">
        <span class="flex h-9 w-9 items-center justify-center rounded-full border-4 border-white/40 bg-white/80 shadow-inner"><span class="h-4 w-4 rounded-full bg-white"></span></span>
      </button>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import { adminMenuIcon } from '@/components/icons/adminMenuIcons'
import { useAuthStore } from '@/stores/auth'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { clampQuickActionPosition } from '@/utils/adminQuickActions'

const authStore = useAuthStore()
const settings = useAdminSettingsStore()
const route = useRoute()
const items = computed(() => settings.adminQuickActions)
const expanded = ref(false)
const viewport = reactive({ width: window.innerWidth, height: window.innerHeight })
const position = reactive(clampQuickActionPosition(viewport.width - 72, viewport.height / 2 - 28, viewport.width, viewport.height))
let drag: { id: number; x: number; y: number; left: number; top: number } | null = null
let suppressClick = false
const panelStyle = computed(() => {
  const width = Math.min(224, viewport.width - 16)
  const height = Math.min(items.value.length * 48 + 16, viewport.height - 16)
  return {
    width: `${width}px`, maxHeight: `${viewport.height - 16}px`,
    left: `${Math.max(8, Math.min(position.x > viewport.width / 2 ? position.x - width - 8 : position.x + 64, viewport.width - width - 8))}px`,
    top: `${Math.max(8, Math.min(position.y - height / 2 + 28, viewport.height - height - 8))}px`
  }
})

function startDrag(event: PointerEvent) {
  if (!event.isPrimary || event.button !== 0) return
  suppressClick = false
  drag = { id: event.pointerId, x: event.clientX, y: event.clientY, left: position.x, top: position.y }
  ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
}
function moveDrag(event: PointerEvent) {
  if (!drag || drag.id !== event.pointerId) return
  const dx = event.clientX - drag.x
  const dy = event.clientY - drag.y
  if (!suppressClick && Math.hypot(dx, dy) < 6) return
  suppressClick = true
  expanded.value = false
  Object.assign(position, clampQuickActionPosition(drag.left + dx, drag.top + dy, viewport.width, viewport.height))
}
function endDrag(event: PointerEvent) {
  if (!drag || drag.id !== event.pointerId) return
  drag = null
  try { localStorage.setItem('admin_quick_actions_position', JSON.stringify(position)) } catch { /* 浏览器禁止存储时仍可拖动。 */ }
}
function cancelDrag() { drag = null }
function toggle(event: MouseEvent) {
  if (suppressClick && event.detail !== 0) { suppressClick = false; return }
  expanded.value = !expanded.value
}
function resize() {
  viewport.width = window.innerWidth
  viewport.height = window.innerHeight
  Object.assign(position, clampQuickActionPosition(position.x, position.y, viewport.width, viewport.height))
}
function closeOnEscape(event: KeyboardEvent) { if (event.key === 'Escape') expanded.value = false }
watch(() => route.fullPath, () => { expanded.value = false })
onMounted(() => {
  try {
    const saved = JSON.parse(localStorage.getItem('admin_quick_actions_position') || 'null')
    if (Number.isFinite(saved?.x) && Number.isFinite(saved?.y)) Object.assign(position, clampQuickActionPosition(saved.x, saved.y, viewport.width, viewport.height))
  } catch { /* 无有效缓存时使用右侧中间位置。 */ }
  if (authStore.isAdmin) void settings.fetch()
  window.addEventListener('resize', resize)
  window.addEventListener('keydown', closeOnEscape)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', resize)
  window.removeEventListener('keydown', closeOnEscape)
})
</script>
