<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import {
  opsAPI,
  type AlertEmailDelivery,
  type AlertEmailDeliveryStatus,
  type AlertEvent,
  type EmailNotificationConfig
} from '@/api/admin/ops'
import { formatDateTime } from '../utils/opsFormatters'

const emit = defineEmits<{
  openError: [errorId: number]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const isDesktop = useMediaQuery('(min-width: 768px)')

const loading = ref(false)
const savingSwitch = ref(false)
const items = ref<AlertEmailDelivery[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const status = ref<AlertEmailDeliveryStatus | ''>('')
const config = ref<EmailNotificationConfig | null>(null)
const selectedDelivery = ref<AlertEmailDelivery | null>(null)
const selectedAlert = ref<AlertEvent | null>(null)
const detailLoading = ref(false)

const statusOptions = computed(() => [
  { value: '', label: t('admin.ops.alertEmailDeliveries.status.all') },
  { value: 'sent', label: t('admin.ops.alertEmailDeliveries.status.sent') },
  { value: 'failed', label: t('admin.ops.alertEmailDeliveries.status.failed') },
  { value: 'quiet_hours', label: t('admin.ops.alertEmailDeliveries.status.quietHours') },
  { value: 'disabled', label: t('admin.ops.alertEmailDeliveries.status.disabled') },
  { value: 'rate_limited', label: t('admin.ops.alertEmailDeliveries.status.rateLimited') },
  { value: 'silenced', label: t('admin.ops.alertEmailDeliveries.status.silenced') }
])

async function loadConfig() {
  try {
    config.value = await opsAPI.getEmailNotificationConfig()
  } catch (error) {
    console.error('[OpsAlertEmailDeliveriesCard] 加载邮件配置失败', error)
  }
}

async function loadDeliveries() {
  loading.value = true
  try {
    const result = await opsAPI.listAlertEmailDeliveries({
      status: status.value,
      page: page.value,
      page_size: pageSize.value
    })
    items.value = result.items || []
    total.value = result.total || 0
  } catch (error: any) {
    console.error('[OpsAlertEmailDeliveriesCard] 加载邮件投递记录失败', error)
    appStore.showError(error?.response?.data?.detail || t('admin.ops.alertEmailDeliveries.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function refreshAll() {
  await Promise.all([loadConfig(), loadDeliveries()])
}

async function toggleEmailEnabled(enabled: boolean) {
  if (!config.value || savingSwitch.value) return
  const previous = config.value.alert.enabled
  config.value.alert.enabled = enabled
  savingSwitch.value = true
  try {
    config.value = await opsAPI.updateEmailNotificationConfig(config.value)
    appStore.showSuccess(t(enabled ? 'admin.ops.alertEmailDeliveries.enabled' : 'admin.ops.alertEmailDeliveries.disabled'))
  } catch (error: any) {
    config.value.alert.enabled = previous
    appStore.showError(error?.response?.data?.detail || t('admin.ops.alertEmailDeliveries.switchFailed'))
  } finally {
    savingSwitch.value = false
  }
}

function deliveryStatusLabel(value: AlertEmailDeliveryStatus) {
  const keyMap: Record<AlertEmailDeliveryStatus, string> = {
    sent: 'sent',
    failed: 'failed',
    quiet_hours: 'quietHours',
    disabled: 'disabled',
    rate_limited: 'rateLimited',
    silenced: 'silenced'
  }
  return t(`admin.ops.alertEmailDeliveries.status.${keyMap[value]}`)
}

function deliveryStatusClass(value: AlertEmailDeliveryStatus) {
  if (value === 'sent') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
  if (value === 'failed') return 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
  if (value === 'quiet_hours' || value === 'silenced') return 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300'
  return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
}

function compactText(value: string, fallback = '-') {
  return String(value || '').trim() || fallback
}

function setStatus(value: string | number | boolean | null) {
  status.value = typeof value === 'string' ? value as AlertEmailDeliveryStatus | '' : ''
}

async function openDeliveryDetail(item: AlertEmailDelivery) {
  selectedDelivery.value = item
  selectedAlert.value = null
  if (!item.alert_event_id) return
  detailLoading.value = true
  try {
    selectedAlert.value = await opsAPI.getAlertEvent(item.alert_event_id)
  } catch (error) {
    console.error('[OpsAlertEmailDeliveriesCard] 加载关联告警失败', error)
  } finally {
    detailLoading.value = false
  }
}

function closeDetail() {
  selectedDelivery.value = null
  selectedAlert.value = null
}

watch(status, () => {
  page.value = 1
  loadDeliveries()
})
watch(page, loadDeliveries)
watch(pageSize, () => {
  page.value = 1
  loadDeliveries()
})

onMounted(refreshAll)
</script>

<template>
  <section class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <Icon name="mail" size="sm" class="text-gray-500 dark:text-gray-400" />
          <h3 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.ops.alertEmailDeliveries.title') }}</h3>
        </div>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.ops.alertEmailDeliveries.quietHours', {
            start: config?.alert.quiet_hours_start || '23:00',
            end: config?.alert.quiet_hours_end || '08:00'
          }) }}
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <label class="flex items-center gap-2 text-xs font-semibold text-gray-700 dark:text-gray-300">
          <span>{{ t('admin.ops.alertEmailDeliveries.masterSwitch') }}</span>
          <Toggle
            :model-value="Boolean(config?.alert.enabled)"
            :disabled="savingSwitch"
            @update:model-value="toggleEmailEnabled"
          />
        </label>
        <Select
          :model-value="status"
          :options="statusOptions"
          class="w-[132px]"
          @change="setStatus"
        />
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" :title="t('common.refresh')" @click="refreshAll">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
          <span>{{ t('common.refresh') }}</span>
        </button>
      </div>
    </div>

    <div v-if="loading && !items.length" class="py-12 text-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('common.loading') }}
    </div>
    <EmptyState v-else-if="!items.length" :title="t('admin.ops.alertEmailDeliveries.empty')" />

    <div v-else-if="!isDesktop" class="divide-y divide-gray-100 dark:divide-dark-700">
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        class="block w-full space-y-2 px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-dark-700/50"
        @click="openDeliveryDetail(item)"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="break-words text-sm font-semibold text-gray-900 dark:text-white">{{ item.rule_name || item.subject }}</div>
            <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ formatDateTime(item.created_at) }}</div>
          </div>
          <span class="shrink-0 rounded-md px-2 py-1 text-[11px] font-bold" :class="deliveryStatusClass(item.status)">
            {{ deliveryStatusLabel(item.status) }}
          </span>
        </div>
        <div class="grid grid-cols-1 gap-1 text-xs text-gray-600 dark:text-gray-300">
          <div>{{ t('admin.ops.alertEmailDeliveries.columns.recipient') }}：{{ compactText(item.recipient_email) }}</div>
          <div>{{ t('admin.ops.alertEmailDeliveries.columns.targetSite') }}：{{ compactText(item.target_site) }}</div>
          <div>{{ t('admin.ops.alertEmailDeliveries.columns.account') }}：{{ compactText(item.account_summary) }}</div>
          <div v-if="item.failure_reason" class="break-words text-red-600 dark:text-red-400">{{ item.failure_reason }}</div>
        </div>
      </button>
    </div>

    <div v-else class="overflow-x-auto">
      <table class="min-w-[1120px] w-full table-fixed divide-y divide-gray-200 text-left dark:divide-dark-700">
        <thead class="bg-gray-50 text-[11px] font-bold uppercase text-gray-500 dark:bg-dark-900 dark:text-gray-400">
          <tr>
            <th class="w-[14%] px-3 py-2">{{ t('admin.ops.alertEmailDeliveries.columns.time') }}</th>
            <th class="w-[10%] px-3 py-2">{{ t('admin.ops.alertEmailDeliveries.columns.status') }}</th>
            <th class="w-[16%] px-3 py-2">{{ t('admin.ops.alertEmailDeliveries.columns.recipient') }}</th>
            <th class="w-[16%] px-3 py-2">{{ t('admin.ops.alertEmailDeliveries.columns.rule') }}</th>
            <th class="w-[7%] px-3 py-2">{{ t('admin.ops.alertEmailDeliveries.columns.severity') }}</th>
            <th class="w-[16%] px-3 py-2">{{ t('admin.ops.alertEmailDeliveries.columns.targetSite') }}</th>
            <th class="w-[13%] px-3 py-2">{{ t('admin.ops.alertEmailDeliveries.columns.account') }}</th>
            <th class="w-[8%] px-3 py-2 text-center">{{ t('admin.ops.alertEmailDeliveries.columns.action') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="item in items" :key="item.id" class="text-xs text-gray-700 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-dark-700/50">
            <td class="whitespace-nowrap px-3 py-2">{{ formatDateTime(item.created_at) }}</td>
            <td class="px-3 py-2">
              <span class="inline-flex rounded-md px-2 py-1 text-[11px] font-bold" :class="deliveryStatusClass(item.status)">
                {{ deliveryStatusLabel(item.status) }}
              </span>
            </td>
            <td class="break-all px-3 py-2">{{ compactText(item.recipient_email) }}</td>
            <td class="break-words px-3 py-2 font-semibold">{{ compactText(item.rule_name || item.subject) }}</td>
            <td class="px-3 py-2 font-mono">{{ compactText(item.severity) }}</td>
            <td class="break-all px-3 py-2">{{ compactText(item.target_site) }}</td>
            <td class="break-words px-3 py-2">{{ compactText(item.account_summary) }}</td>
            <td class="px-3 py-2 text-center">
              <button type="button" class="btn btn-ghost btn-icon-sm" :title="t('common.view')" @click="openDeliveryDetail(item)">
                <Icon name="eye" size="sm" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Pagination
      v-if="total > 0"
      :total="total"
      :page="page"
      :page-size="pageSize"
      :show-page-size-selector="false"
      @update:page="page = $event"
      @update:page-size="pageSize = $event"
    />

    <BaseDialog :show="Boolean(selectedDelivery)" :title="t('admin.ops.alertEmailDeliveries.detailTitle')" width="wide" @close="closeDetail">
      <div v-if="selectedDelivery" class="space-y-4 text-sm text-gray-700 dark:text-gray-200">
        <dl class="grid grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-2">
          <div><dt class="text-xs text-gray-500">{{ t('admin.ops.alertEmailDeliveries.columns.time') }}</dt><dd>{{ formatDateTime(selectedDelivery.created_at) }}</dd></div>
          <div><dt class="text-xs text-gray-500">{{ t('admin.ops.alertEmailDeliveries.columns.status') }}</dt><dd>{{ deliveryStatusLabel(selectedDelivery.status) }}</dd></div>
          <div><dt class="text-xs text-gray-500">{{ t('admin.ops.alertEmailDeliveries.columns.recipient') }}</dt><dd class="break-all">{{ compactText(selectedDelivery.recipient_email) }}</dd></div>
          <div><dt class="text-xs text-gray-500">{{ t('admin.ops.alertEmailDeliveries.columns.rule') }}</dt><dd>{{ compactText(selectedDelivery.rule_name) }}</dd></div>
          <div class="sm:col-span-2"><dt class="text-xs text-gray-500">{{ t('admin.ops.alertEmailDeliveries.columns.targetSite') }}</dt><dd class="break-all">{{ compactText(selectedDelivery.target_site) }}</dd></div>
          <div class="sm:col-span-2"><dt class="text-xs text-gray-500">{{ t('admin.ops.alertEmailDeliveries.columns.account') }}</dt><dd class="break-words">{{ compactText(selectedDelivery.account_summary) }}</dd></div>
          <div v-if="selectedDelivery.failure_reason" class="sm:col-span-2"><dt class="text-xs text-gray-500">{{ t('admin.ops.alertEmailDeliveries.failureReason') }}</dt><dd class="whitespace-pre-wrap break-words text-red-600 dark:text-red-400">{{ selectedDelivery.failure_reason }}</dd></div>
        </dl>

        <div v-if="selectedDelivery.detail_html" class="border-t border-gray-200 pt-4 dark:border-dark-700">
          <div class="mb-2 text-xs font-semibold text-gray-500">{{ t('admin.ops.alertEmailDeliveries.mailDetail') }}</div>
          <!-- 后端只返回经过 HTML 转义和凭据脱敏的邮件详情。 -->
          <div class="max-h-[28rem] overflow-auto rounded-md border border-gray-200 bg-white p-3 text-xs text-gray-800 dark:border-dark-600 dark:bg-dark-900 dark:text-gray-200" v-html="selectedDelivery.detail_html" />
        </div>

        <div v-if="detailLoading" class="py-4 text-center text-xs text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="selectedAlert" class="border-t border-gray-200 pt-4 dark:border-dark-700">
          <div class="text-sm font-bold text-gray-900 dark:text-white">{{ selectedAlert.title }}</div>
          <div class="mt-1 whitespace-pre-wrap break-words text-xs text-gray-600 dark:text-gray-300">{{ selectedAlert.description }}</div>
        </div>

        <div v-if="selectedDelivery.error_ids?.length" class="flex flex-wrap gap-2 border-t border-gray-200 pt-4 dark:border-dark-700">
          <button
            v-for="errorId in selectedDelivery.error_ids"
            :key="errorId"
            type="button"
            class="btn btn-secondary btn-sm"
            @click="emit('openError', errorId)"
          >
            {{ t('admin.ops.alertEmailDeliveries.openError', { id: errorId }) }}
            <Icon name="externalLink" size="xs" />
          </button>
        </div>
      </div>
    </BaseDialog>
  </section>
</template>
