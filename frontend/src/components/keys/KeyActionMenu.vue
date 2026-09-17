<template>
  <button ref="trigger" type="button" :aria-expanded="open" aria-haspopup="menu" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400" @click="toggle">
    <Icon name="more" size="sm" />
    <span class="text-xs">{{ t('common.more') }}</span>
  </button>
  <Teleport to="body">
    <template v-if="open">
      <div class="fixed inset-0 z-40" @click="close(false)" />
      <div ref="menu" role="menu" :aria-label="t('common.more')" class="fixed z-50 w-36 rounded-xl bg-white py-1 shadow-lg ring-1 ring-black/5 dark:bg-dark-800" :style="position" @keydown.esc.prevent.stop="close()" @keydown.down.prevent="focusNext(1)" @keydown.up.prevent="focusNext(-1)" @focusout="onFocusOut">
        <button role="menuitem" type="button" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700" @click="emit('toggle'); close()">
          <Icon :name="active ? 'ban' : 'checkCircle'" size="sm" />
          {{ active ? t('keys.disable') : t('keys.enable') }}
        </button>
        <button role="menuitem" type="button" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20" @click="close(); emit('delete')">
          <Icon name="trash" size="sm" />
          {{ t('common.delete') }}
        </button>
      </div>
    </template>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps<{ active: boolean }>()
const emit = defineEmits<{ toggle: []; delete: [] }>()
const { t } = useI18n()
const trigger = ref<HTMLButtonElement | null>(null)
const menu = ref<HTMLElement | null>(null)
const open = ref(false)
const position = ref({ top: '0px', left: '0px' })

function close(restoreFocus = true) {
  open.value = false
  window.removeEventListener('resize', dismiss)
  window.removeEventListener('scroll', dismiss, true)
  if (restoreFocus) trigger.value?.focus()
}
function dismiss() { close(false) }
async function toggle() {
  if (open.value) { close(); return }
  open.value = true
  await nextTick()
  if (!trigger.value || !menu.value) return
  // 传送到 body 并按视口定位，避免表格横向滚动和最后一行裁剪菜单。
  const anchor = trigger.value.getBoundingClientRect()
  const { width, height } = menu.value.getBoundingClientRect()
  const top = anchor.bottom + height + 4 <= window.innerHeight - 8 ? anchor.bottom + 4 : anchor.top - height - 4
  position.value = {
    top: `${Math.max(8, top)}px`,
    left: `${Math.max(8, Math.min(anchor.right - width, window.innerWidth - width - 8))}px`
  }
  menu.value.querySelector('button')?.focus()
  window.addEventListener('resize', dismiss)
  window.addEventListener('scroll', dismiss, true)
}
function focusNext(direction: number) {
  const buttons = Array.from(menu.value?.querySelectorAll('button') || [])
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement)
  buttons[(current + direction + buttons.length) % buttons.length]?.focus()
}
function onFocusOut(event: FocusEvent) {
  if (!menu.value?.contains(event.relatedTarget as Node | null)) close(false)
}
onUnmounted(dismiss)
</script>
