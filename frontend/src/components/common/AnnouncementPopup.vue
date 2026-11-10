<template>
  <Teleport to="body">
    <Transition name="popup-fade">
      <div
        v-if="isVisible"
        class="fixed inset-0 z-[120] flex items-center justify-center bg-black/65 p-2 backdrop-blur-sm sm:p-4"
      >
        <div
          class="announcement-popup-dialog flex max-h-[92vh] w-[90vw] max-w-[1280px] flex-col overflow-hidden rounded-2xl bg-white shadow-2xl ring-1 ring-black/5 dark:bg-dark-800 dark:ring-white/10"
          @click.stop
        >
          <div class="flex shrink-0 items-center justify-between border-b border-gray-100 bg-gray-50/70 px-5 py-4 dark:border-dark-700 dark:bg-dark-900/40 sm:px-8">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('announcements.centerTitle') }}
            </h2>
            <button
              type="button"
              class="flex h-9 w-9 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-200 hover:text-gray-800 dark:hover:bg-dark-700 dark:hover:text-white"
              :aria-label="t('common.close')"
              @click="handleDismiss"
            >
              <Icon name="x" size="md" />
            </button>
          </div>

          <div class="min-h-0 flex-1 overflow-y-auto bg-white px-4 py-5 dark:bg-dark-800 sm:px-8 sm:py-7">
            <div class="relative">
              <article
                v-for="(item, index) in timelineAnnouncements"
                :key="item.id ?? `${item.created_at}-${index}`"
                data-testid="announcement-timeline-item"
                class="relative pb-6 pl-7 sm:pb-8 sm:pl-10"
              >
                <span class="absolute left-0 top-2 z-10 h-3 w-3 rounded-full border-2 border-white bg-emerald-500 ring-1 ring-emerald-200 dark:border-dark-800 dark:ring-emerald-900" />
                <span
                  v-if="index < timelineAnnouncements.length - 1"
                  class="absolute bottom-0 left-[5px] top-5 w-px bg-gray-200 dark:bg-dark-600"
                />
                <div class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1">
                  <h3 class="min-w-0 flex-1 text-base font-semibold leading-6 text-gray-900 dark:text-white sm:text-lg">
                    {{ item.title }}
                  </h3>
                  <span
                    v-if="preview && item.status"
                    class="rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
                  >
                    {{ t(`admin.announcements.statusLabels.${item.status}`) }}
                  </span>
                </div>
                <div
                  class="markdown-body max-w-none text-sm text-gray-700 dark:text-gray-200 sm:text-base"
                  v-html="item.renderedContent"
                ></div>
                <time class="mt-3 block text-sm text-gray-500 dark:text-gray-400">
                  {{ formatRelativeWithDateTime(item.created_at) }}
                </time>
              </article>
            </div>
          </div>

          <div class="flex shrink-0 justify-end border-t border-gray-100 bg-gray-50/70 px-5 py-4 dark:border-dark-700 dark:bg-dark-900/40 sm:px-8">
            <div class="flex items-center justify-end">
              <button
                @click="handleDismiss"
                data-testid="announcement-popup-dismiss"
                class="btn btn-primary min-w-20"
              >
                <span class="flex items-center gap-2">
                  <Icon :name="preview ? 'x' : 'check'" size="sm" />
                  {{ preview ? t('common.close') : t('announcements.markAllRead') }}
                </span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeWithDateTime } from '@/utils/format'
import Icon from '@/components/icons/Icon.vue'
import type { Announcement, UserAnnouncement } from '@/types'
import '@/styles/announcement-markdown.css'

type PreviewAnnouncement = Pick<Announcement | UserAnnouncement, 'id' | 'title' | 'content' | 'created_at'>
  & Partial<Pick<Announcement, 'status'>>

const props = withDefaults(defineProps<{
  announcement?: PreviewAnnouncement | null
  preview?: boolean
}>(), {
  announcement: null,
  preview: false,
})

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const announcementStore = useAnnouncementStore()
const isVisible = computed(() => (
  props.preview ? Boolean(props.announcement) : Boolean(announcementStore.currentPopup)
))
const displayedAnnouncements = computed<PreviewAnnouncement[]>(() => (
  props.preview
    ? (props.announcement ? [props.announcement] : [])
    : announcementStore.announcements
))

marked.setOptions({
  breaks: true,
  gfm: true,
})

const timelineAnnouncements = computed(() => displayedAnnouncements.value
  .map((announcement) => ({
    ...announcement,
    renderedContent: announcement.content
      ? DOMPurify.sanitize(marked.parse(announcement.content) as string)
      : '',
  }))
  .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at) || (b.id ?? 0) - (a.id ?? 0))
)

function handleDismiss() {
  if (props.preview) {
    emit('close')
    return
  }
  announcementStore.dismissPopup()
}

// Manage body overflow — only set, never unset (bell component handles restore)
watch(
  isVisible,
  (visible) => {
    if (visible) {
      document.body.style.overflow = 'hidden'
    } else if (props.preview) {
      document.body.style.overflow = ''
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (props.preview) {
    document.body.style.overflow = ''
  }
})
</script>

<style scoped>
.popup-fade-enter-active {
  transition: all 0.3s cubic-bezier(0.16, 1, 0.3, 1);
}

.popup-fade-leave-active {
  transition: all 0.2s cubic-bezier(0.4, 0, 1, 1);
}

.popup-fade-enter-from,
.popup-fade-leave-to {
  opacity: 0;
}

.popup-fade-enter-from > div {
  transform: scale(0.94) translateY(-12px);
  opacity: 0;
}

.popup-fade-leave-to > div {
  transform: scale(0.96) translateY(-8px);
  opacity: 0;
}

/* Scrollbar Styling */
.overflow-y-auto::-webkit-scrollbar {
  width: 8px;
}

.overflow-y-auto::-webkit-scrollbar-track {
  background: transparent;
}

.overflow-y-auto::-webkit-scrollbar-thumb {
  background: linear-gradient(to bottom, #cbd5e1, #94a3b8);
  border-radius: 4px;
}

.dark .overflow-y-auto::-webkit-scrollbar-thumb {
  background: linear-gradient(to bottom, #4b5563, #374151);
}
</style>
