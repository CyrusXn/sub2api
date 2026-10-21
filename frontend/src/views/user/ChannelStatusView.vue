<template>
  <div v-if="!settingsReady" role="status" class="flex min-h-[240px] flex-col items-center justify-center gap-3">
    <button v-if="loadFailed" type="button" class="btn btn-secondary" @click="refreshSettings">
      {{ t('common.tryAgain') }}
    </button>
    <LoadingSpinner v-else size="lg" />
  </div>
  <ChannelStatusV2View v-else-if="mode === 'v2'" />
  <ChannelStatusV1View v-else />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getChannelMonitorMode } from '@/utils/featureFlags'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusV2View from './ChannelStatusV2View.vue'

const { t } = useI18n()
const appStore = useAppStore()
const mode = computed(getChannelMonitorMode)
const checkedSettings = ref(false)
const settingsReady = computed(() => checkedSettings.value && appStore.cachedPublicSettings !== null)
const loadFailed = ref(false)

async function refreshSettings() {
  loadFailed.value = false
  // 独立于全局 setup 检查加载配置；进入页面也重新核对，避免旧会话停留在旧模式。
  const settings = await appStore.fetchPublicSettings(true)
  // 刷新失败时仍允许已有配置降级；首次无配置则显示重试。
  checkedSettings.value = true
  loadFailed.value = !settings && !settingsReady.value
}

onMounted(refreshSettings)
</script>
