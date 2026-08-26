<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
          <div class="relative w-full sm:w-72">
            <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input
              v-model="searchQuery"
              type="search"
              class="input pl-10"
              :placeholder="t('admin.accounts.upstreamSites.search')"
            />
          </div>
          <button
            type="button"
            class="btn btn-secondary self-end sm:self-auto"
            :title="t('common.refresh')"
            :disabled="loading"
            @click="loadSites"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="filteredSites"
          :loading="loading"
          row-key="host"
          column-width-storage-key="upstream-site-table-column-widths"
          column-order-storage-key="upstream-site-table-column-order"
        >
          <template #cell-website_url="{ row }">
            <a
              :href="row.website_url"
              target="_blank"
              rel="noopener noreferrer"
              class="font-medium text-primary-600 hover:underline dark:text-primary-400"
            >
              {{ row.website_url }}
            </a>
          </template>

          <template #cell-display_name="{ row }">
            <span class="font-medium text-gray-900 dark:text-white">{{ row.display_name || row.host }}</span>
          </template>

          <template #cell-account_names="{ row }">
            <div class="flex flex-wrap gap-1.5">
              <span
                v-for="name in row.account_names"
                :key="name"
                class="rounded bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-700 dark:text-gray-200"
              >
                {{ name }}
              </span>
            </div>
          </template>

          <template #cell-protocol="{ row }">
            <span class="font-mono text-xs uppercase text-gray-600 dark:text-gray-300">{{ row.protocol }}</span>
          </template>

          <template #cell-login_username="{ row }">
            <span v-if="row.login_username" class="text-gray-900 dark:text-white">{{ row.login_username }}</span>
            <span v-else class="text-gray-400">{{ t('common.notAvailable') }}</span>
          </template>

          <template #cell-password_status="{ row }">
            <span
              class="inline-flex items-center gap-1.5 text-sm font-medium"
              :class="row.has_password ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'"
            >
              <span class="h-2 w-2 rounded-full bg-current"></span>
              {{ row.has_password
                ? t('admin.accounts.upstreamSites.configured')
                : t('admin.accounts.upstreamSites.notConfigured') }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <button
              type="button"
              class="btn btn-secondary px-3 py-1.5 text-sm"
              data-test="edit-site"
              @click="openEdit(row)"
            >
              <Icon name="edit" size="sm" class="mr-1.5" />
              {{ t('common.edit') }}
            </button>
          </template>
        </DataTable>
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showEditDialog"
      :title="t('admin.accounts.upstreamSites.editTitle')"
      width="normal"
      @close="closeEdit"
    >
      <form class="space-y-5" @submit.prevent="saveCredential">
        <div>
          <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">站点名称</label>
          <input v-model.trim="form.display_name" type="text" class="input" maxlength="255" data-test="display-name" />
        </div>
        <div>
          <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">
            {{ t('admin.accounts.upstreamSites.website') }}
          </label>
          <input :value="editingSite?.website_url" class="input bg-gray-50 dark:bg-dark-800" disabled />
        </div>
        <div>
          <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">
            {{ t('admin.accounts.upstreamSites.loginUsername') }}
          </label>
          <input
            v-model.trim="form.login_username"
            type="text"
            class="input"
            autocomplete="username"
            required
            data-test="login-username"
          />
        </div>
        <div>
          <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">
            {{ t('admin.accounts.upstreamSites.loginPassword') }}
          </label>
          <input
            v-model="form.login_password"
            type="password"
            class="input"
            autocomplete="new-password"
            :required="!editingSite?.has_password"
            :placeholder="editingSite?.has_password ? t('admin.accounts.upstreamSites.passwordKeepPlaceholder') : ''"
            data-test="login-password"
          />
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.upstreamSites.passwordHint') }}
          </p>
        </div>
      </form>

      <template #footer>
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="closeEdit">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="saving || !form.login_username || (!editingSite?.has_password && !form.login_password)"
          data-test="save-site"
          @click="saveCredential"
        >
          <Icon v-if="saving" name="refresh" size="sm" class="mr-1.5 animate-spin" />
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { UpstreamSiteCredentialSummary } from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(false)
const saving = ref(false)
const sites = ref<UpstreamSiteCredentialSummary[]>([])
const searchQuery = ref('')
const showEditDialog = ref(false)
const editingSite = ref<UpstreamSiteCredentialSummary | null>(null)
const form = reactive({ display_name: '', login_username: '', login_password: '' })
const isReadOnlyPreview = () => import.meta.env.VITE_READ_ONLY_PREVIEW === 'true'

const columns = computed<Column[]>(() => [
  { key: 'display_name', label: '站点名称', width: 160 },
  { key: 'website_url', label: t('admin.accounts.upstreamSites.columns.website'), width: 240 },
  { key: 'account_names', label: t('admin.accounts.upstreamSites.columns.accounts'), width: 320 },
  { key: 'protocol', label: t('admin.accounts.upstreamSites.columns.protocol'), width: 110 },
  { key: 'login_username', label: t('admin.accounts.upstreamSites.columns.loginUsername'), width: 220 },
  { key: 'password_status', label: t('admin.accounts.upstreamSites.columns.passwordStatus'), width: 120 },
  { key: 'actions', label: t('common.actions'), width: 120 }
])

const filteredSites = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return sites.value
  return sites.value.filter(site =>
    site.display_name.toLowerCase().includes(query) ||
    site.website_url.toLowerCase().includes(query) ||
    site.login_username.toLowerCase().includes(query) ||
    site.account_names.some(name => name.toLowerCase().includes(query))
  )
})

const loadSites = async () => {
  loading.value = true
  try {
    sites.value = await adminAPI.accounts.listUpstreamSites()
  } catch (error) {
    console.error('加载上游站点账号失败:', error)
    appStore.showError(t('admin.accounts.upstreamSites.loadFailed'))
  } finally {
    loading.value = false
  }
}

const openEdit = (site: UpstreamSiteCredentialSummary) => {
  editingSite.value = site
  form.display_name = site.display_name || site.host
  form.login_username = site.login_username
  form.login_password = ''
  showEditDialog.value = true
}

const closeEdit = () => {
  if (saving.value) return
  showEditDialog.value = false
  editingSite.value = null
  form.login_username = ''
  form.display_name = ''
  form.login_password = ''
}

const saveCredential = async () => {
  if (!editingSite.value || !form.login_username || (!editingSite.value.has_password && !form.login_password)) return
  // 本地连接线上 API 的验收模式只允许查看，避免误写线上凭据。
  if (isReadOnlyPreview()) {
    appStore.showInfo(t('admin.accounts.upstreamSites.readOnlyPreview'))
    return
  }
  saving.value = true
  try {
    await adminAPI.accounts.upsertUpstreamSiteCredential({
      base_url: editingSite.value.website_url,
      display_name: form.display_name,
      login_username: form.login_username,
      login_password: form.login_password
    })
    appStore.showSuccess(t('admin.accounts.upstreamSites.saved'))
    showEditDialog.value = false
    editingSite.value = null
    form.login_password = ''
    await loadSites()
  } catch (error) {
    console.error('保存上游站点账号失败:', error)
    appStore.showError(t('admin.accounts.upstreamSites.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(loadSites)
</script>
