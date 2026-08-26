<template>
  <div :class="flat ? 'p-4 sm:p-6' : 'card p-6'">
    <!-- Toolbar: left filters (multi-line) + right actions -->
    <div class="flex flex-wrap items-end justify-between gap-4">
      <!-- Left: filters (allowed to wrap to multiple rows) -->
      <div class="flex flex-1 flex-wrap items-end gap-4">
        <!-- User Search -->
        <div ref="userSearchRef" class="usage-filter-dropdown relative w-full sm:w-auto sm:min-w-[240px]">
          <label class="input-label">{{ t('admin.usage.userFilter') }}</label>
          <input
            v-model="userKeyword"
            type="text"
            autocomplete="off"
            autocorrect="off"
            autocapitalize="none"
            spellcheck="false"
            class="input pr-8"
            :placeholder="t('admin.usage.searchUserPlaceholder')"
            @input="debounceUserSearch"
            @focus="showUserDropdown = true"
          />
          <button
            v-if="filters.user_ids?.length"
            type="button"
            @click="clearUser"
            class="absolute right-2 top-9 text-gray-400"
            aria-label="清除用户筛选"
          >
            ✕
          </button>
          <div v-if="selectedUsers.length" class="mt-2 flex flex-wrap gap-1.5">
            <button
              v-for="user in selectedUsers"
              :key="user.id"
              type="button"
              class="inline-flex max-w-full items-center gap-1 rounded border border-gray-200 bg-gray-50 px-2 py-1 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-200"
              @click="removeUser(user.id)"
            >
              <span class="truncate">{{ user.email }}</span><span aria-hidden="true">×</span>
            </button>
          </div>
          <div
            v-if="showUserDropdown && (userResults.length > 0 || userKeyword)"
            class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800"
          >
            <button
              v-for="u in userResults"
              :key="u.id"
              type="button"
              @click="selectUser(u)"
              class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <span>{{ u.email }}<span v-if="u.deleted" class="ml-1 text-xs text-gray-400">（{{ t('admin.usage.userDeletedBadge') }}）</span></span>
              <span class="ml-2 text-xs text-gray-400">#{{ u.id }}</span>
            </button>
          </div>
        </div>

        <!-- API Key Search -->
        <div ref="apiKeySearchRef" class="usage-filter-dropdown relative w-full sm:w-auto sm:min-w-[240px]">
          <label class="input-label">{{ t('usage.apiKeyFilter') }}</label>
          <input
            v-model="apiKeyKeyword"
            type="text"
            autocomplete="off"
            autocorrect="off"
            autocapitalize="none"
            spellcheck="false"
            class="input pr-8"
            :placeholder="t('admin.usage.searchApiKeyPlaceholder')"
            @input="debounceApiKeySearch"
            @focus="onApiKeyFocus"
          />
          <button
            v-if="filters.api_key_ids?.length"
            type="button"
            @click="onClearApiKey"
            class="absolute right-2 top-9 text-gray-400"
            aria-label="清除 API 密钥筛选"
          >
            ✕
          </button>
          <div v-if="selectedApiKeys.length" class="mt-2 flex flex-wrap gap-1.5">
            <button
              v-for="key in selectedApiKeys"
              :key="key.id"
              type="button"
              class="inline-flex max-w-full items-center gap-1 rounded border border-gray-200 bg-gray-50 px-2 py-1 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-200"
              @click="removeApiKey(key.id)"
            >
              <span class="truncate">{{ key.name || `#${key.id}` }}</span><span aria-hidden="true">×</span>
            </button>
          </div>
          <div
            v-if="showApiKeyDropdown && apiKeyResults.length > 0"
            class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800"
          >
            <button
              v-for="k in apiKeyResults"
              :key="k.id"
              type="button"
              @click="selectApiKey(k)"
              class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <span class="truncate">{{ k.name || `#${k.id}` }}</span>
              <span class="ml-2 text-xs text-gray-400">#{{ k.id }}</span>
            </button>
          </div>
        </div>

        <!-- Model Filter -->
        <div class="w-full sm:w-auto sm:min-w-[220px]">
          <label class="input-label">{{ t('usage.model') }}</label>
          <Select v-model="filters.models" :options="modelOptions" multiple clearable searchable @change="emitChange" />
        </div>

        <!-- Account Filter -->
        <div ref="accountSearchRef" class="usage-filter-dropdown relative w-full sm:w-auto sm:min-w-[220px]">
          <label class="input-label">{{ t('admin.usage.account') }}</label>
          <input
            v-model="accountKeyword"
            type="text"
            autocomplete="off"
            autocorrect="off"
            autocapitalize="none"
            spellcheck="false"
            class="input pr-8"
            :placeholder="t('admin.usage.searchAccountPlaceholder')"
            @input="debounceAccountSearch"
            @focus="showAccountDropdown = true"
          />
          <button
            v-if="filters.account_ids?.length"
            type="button"
            @click="clearAccount"
            class="absolute right-2 top-9 text-gray-400"
            aria-label="清除账号筛选"
          >
            ✕
          </button>
          <div v-if="selectedAccounts.length" class="mt-2 flex flex-wrap gap-1.5">
            <button
              v-for="account in selectedAccounts"
              :key="account.id"
              type="button"
              class="inline-flex max-w-full items-center gap-1 rounded border border-gray-200 bg-gray-50 px-2 py-1 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-200"
              @click="removeAccount(account.id)"
            >
              <span class="truncate">{{ account.name }}</span><span aria-hidden="true">×</span>
            </button>
          </div>
          <div
            v-if="showAccountDropdown && (accountResults.length > 0 || accountKeyword)"
            class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800"
          >
            <button
              v-for="a in accountResults"
              :key="a.id"
              type="button"
              @click="selectAccount(a)"
              class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <span class="truncate">{{ a.name }}</span>
              <span class="ml-2 text-xs text-gray-400">#{{ a.id }}</span>
            </button>
          </div>
        </div>

        <!-- Request Type Filter (usage only) -->
        <div v-if="mode !== 'errors'" class="w-full sm:w-auto sm:min-w-[180px]">
          <label class="input-label">{{ t('usage.type') }}</label>
          <Select v-model="filters.request_types" :options="requestTypeOptions" multiple clearable @change="emitChange" />
        </div>

        <!-- Billing Type Filter (usage only) -->
        <div v-if="mode !== 'errors'" class="w-full sm:w-auto sm:min-w-[200px]">
          <label class="input-label">{{ t('admin.usage.billingType') }}</label>
          <Select v-model="filters.billing_types" :options="billingTypeOptions" multiple clearable @change="emitChange" />
        </div>

        <!-- Billing Mode Filter (usage only；用户排行的 user-breakdown 接口不支持该维度) -->
        <div v-if="mode === 'usage'" class="w-full sm:w-auto sm:min-w-[200px]">
          <label class="input-label">{{ t('admin.usage.billingMode') }}</label>
          <Select v-model="filters.billing_modes" :options="billingModeOptions" multiple clearable @change="emitChange" />
        </div>

        <div v-if="mode === 'usage'" class="w-full sm:w-auto sm:min-w-[220px]">
          <label class="input-label">{{ t('admin.usage.upstreamModelAudit') }}</label>
          <Select v-model="filters.upstream_model_mismatches" :options="upstreamModelMismatchOptions" multiple clearable @change="emitChange" />
        </div>

        <!-- Error Phase Filter (errors only) -->
        <div v-if="mode === 'errors'" class="w-full sm:w-auto sm:min-w-[180px]">
          <label class="input-label">{{ t('admin.ops.errorLog.type') }}</label>
          <Select v-model="filters.error_phases" :options="errorPhaseOptions" multiple clearable @change="emitChange" />
        </div>

        <!-- Error Category Filter (errors only) -->
        <div v-if="mode === 'errors'" class="w-full sm:w-auto sm:min-w-[180px]">
          <label class="input-label">{{ t('usage.errors.category') }}</label>
          <Select v-model="filters.error_categories" :options="errorCategoryOptions" multiple clearable @change="emitChange" />
        </div>

        <!-- Status Code Filter (errors only) -->
        <div v-if="mode === 'errors'" class="w-full sm:w-auto sm:min-w-[180px]">
          <label class="input-label">{{ t('admin.ops.errorLog.status') }}</label>
          <Select v-model="filters.status_codes" :options="statusCodeOptions" multiple clearable @change="emitChange" />
        </div>

        <!-- Group Filter -->
        <div class="w-full sm:w-auto sm:min-w-[200px]">
          <label class="input-label">{{ t('admin.usage.group') }}</label>
          <Select v-model="filters.group_ids" :options="groupOptions" multiple clearable searchable @change="emitChange" />
        </div>

        <!-- 上游站点按站点绑定的账号集合过滤用量。 -->
        <div v-if="mode === 'usage'" class="w-full sm:w-auto sm:min-w-[220px]">
          <label class="input-label">{{ t('admin.usage.upstreamSite') }}</label>
          <Select
            :model-value="filters.upstream_site_hosts"
            :options="upstreamSiteOptions"
            multiple
            clearable
            searchable
            @update:model-value="selectUpstreamSites"
          />
        </div>

      </div>

      <!-- Right: actions -->
      <div v-if="showActions" class="flex w-full flex-wrap items-center justify-end gap-3 sm:w-auto">
        <button type="button" @click="$emit('refresh')" class="btn btn-secondary">
          {{ t('common.refresh') }}
        </button>
        <button type="button" @click="$emit('reset')" class="btn btn-secondary">
          {{ t('common.reset') }}
        </button>
        <slot name="after-reset" />
        <template v-if="mode === 'usage'">
          <button type="button" @click="$emit('cleanup')" class="btn btn-danger">
            {{ t('admin.usage.cleanup.button') }}
          </button>
          <button type="button" @click="$emit('export')" :disabled="exporting" class="btn btn-primary">
            {{ t('usage.exportExcel') }}
          </button>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import { COMMON_ERROR_STATUS_CODES } from '@/utils/errorBadges'
import type { SimpleApiKey, SimpleUser } from '@/api/admin/usage'

type ModelValue = Record<string, any>

interface Props {
  modelValue: ModelValue
  exporting: boolean
  startDate: string
  endDate: string
  showActions?: boolean
  modelOptions?: string[]
  /**
   * errors 模式:隐藏用量专属字段/按钮,显示错误类型+状态码(错误请求 tab 用)
   * ranking 模式:同 usage 但隐藏计费模式筛选与清理/导出按钮(用户排行 tab 用)
   */
  mode?: 'usage' | 'errors' | 'ranking'
  /** 嵌入统一卡片内使用：去掉自身卡片外观 */
  flat?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  showActions: true,
  mode: 'usage',
  flat: false
})
const emit = defineEmits([
  'update:modelValue',
  'change',
  'refresh',
  'reset',
  'export',
  'cleanup'
])

const { t } = useI18n()

// 筛选控件只能更新本地副本，再通过 v-model 显式同步给父页面；直接改 props
// 会导致父页面组装请求参数时仍使用旧值，所有多选筛选都会失效。
const copyFilters = (value: ModelValue): ModelValue => Object.fromEntries(
  Object.entries(value).map(([key, item]) => [key, Array.isArray(item) ? [...item] : item])
)
const filters = ref<ModelValue>(copyFilters(props.modelValue))
let syncingModelValue = false

watch(
  () => props.modelValue,
  (value) => {
    syncingModelValue = true
    filters.value = copyFilters(value)
    syncingModelValue = false
  },
  { deep: true }
)

watch(
  filters,
  (value) => {
    if (!syncingModelValue) {
      emit('update:modelValue', copyFilters(value))
    }
  },
  { deep: true, flush: 'sync' }
)

const userSearchRef = ref<HTMLElement | null>(null)
const apiKeySearchRef = ref<HTMLElement | null>(null)
const accountSearchRef = ref<HTMLElement | null>(null)

const userKeyword = ref('')
const userResults = ref<SimpleUser[]>([])
const showUserDropdown = ref(false)
let userSearchTimeout: ReturnType<typeof setTimeout> | null = null
let userSearchSequence = 0

const apiKeyKeyword = ref('')
const apiKeyResults = ref<SimpleApiKey[]>([])
const showApiKeyDropdown = ref(false)
let apiKeySearchTimeout: ReturnType<typeof setTimeout> | null = null

interface SimpleAccount {
  id: number
  name: string
}
const selectedUsers = ref<SimpleUser[]>([])
const selectedApiKeys = ref<SimpleApiKey[]>([])
const selectedAccounts = ref<SimpleAccount[]>([])
const accountKeyword = ref('')
const accountResults = ref<SimpleAccount[]>([])
const showAccountDropdown = ref(false)
let accountSearchTimeout: ReturnType<typeof setTimeout> | null = null

const modelOptions = computed<SelectOption[]>(() =>
  (props.modelOptions ?? []).map((m) => ({ value: m, label: m }))
)
const groupOptions = ref<SelectOption[]>([])
const upstreamSites = ref<Array<{ host: string; account_ids: number[] }>>([])
const upstreamSiteOptions = computed<SelectOption[]>(() =>
  upstreamSites.value.map((site) => ({ value: site.host, label: site.host }))
)

const requestTypeOptions = ref<SelectOption[]>([
  { value: 'ws_v2', label: t('usage.ws') },
  { value: 'live', label: t('usage.live') },
  { value: 'stream', label: t('usage.stream') },
  { value: 'sync', label: t('usage.sync') },
  { value: 'cyber', label: t('usage.cyber') }
])

const billingTypeOptions = ref<SelectOption[]>([
  { value: 0, label: t('admin.usage.billingTypeBalance') },
  { value: 1, label: t('admin.usage.billingTypeSubscription') }
])

// 错误类型对应后端 phase 参数(与错误表"类型"徽章同语义)
const errorPhaseOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allTypes') },
  { value: 'upstream', label: t('admin.ops.errorLog.typeUpstream') },
  { value: 'account_auth', label: t('admin.ops.errorLog.typeAccountAuth') },
  { value: 'request', label: t('admin.ops.errorLog.typeRequest') },
  { value: 'auth', label: t('admin.ops.errorLog.typeAuth') },
  { value: 'routing', label: t('admin.ops.errorLog.typeRouting') },
  { value: 'internal', label: t('admin.ops.errorLog.typeInternal') },
])

// 分类码同用户端 /usage 错误筛选;"other" 无法反查为过滤条件,刻意不列
const errorCategoryCodes = ['auth', 'rate_limit', 'quota', 'invalid_request', 'service_unavailable', 'upstream', 'internal', 'cyber']

const errorCategoryOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allCategories') },
  ...errorCategoryCodes.map((c) => ({ value: c, label: t('usage.errors.categories.' + c) })),
])

const statusCodeOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allStatuses') },
  ...COMMON_ERROR_STATUS_CODES.map((c) => ({ value: c, label: String(c) })),
])

const billingModeOptions = ref<SelectOption[]>([
  { value: 'token', label: t('admin.usage.billingModeToken') },
  { value: 'per_request', label: t('admin.usage.billingModePerRequest') },
  { value: 'image', label: t('admin.usage.billingModeImage') },
  { value: 'video', label: t('admin.usage.billingModeVideo') }
])

const upstreamModelMismatchOptions = ref<SelectOption[]>([
  { value: true, label: t('admin.usage.upstreamModelMismatchOnly') },
  { value: false, label: t('admin.usage.upstreamModelMatchedOnly') }
])

const emitChange = () => emit('change')

const clearPendingUserSearch = () => {
  if (userSearchTimeout) {
    clearTimeout(userSearchTimeout)
    userSearchTimeout = null
  }
  userSearchSequence += 1
}

const debounceUserSearch = () => {
  clearPendingUserSearch()
  const query = userKeyword.value.trim()
  if (!query) {
    userResults.value = []
    return
  }

  const sequence = userSearchSequence
  userSearchTimeout = setTimeout(async () => {
    userSearchTimeout = null
    try {
      const results = await adminAPI.usage.searchUsers(query)
      if (sequence === userSearchSequence) {
        userResults.value = results.sort((a, b) => Number(a.deleted) - Number(b.deleted))
      }
    } catch {
      if (sequence === userSearchSequence) {
        userResults.value = []
      }
    }
  }, 300)
}

const debounceApiKeySearch = () => {
  if (apiKeySearchTimeout) clearTimeout(apiKeySearchTimeout)
  apiKeySearchTimeout = setTimeout(async () => {
    try {
      apiKeyResults.value = await adminAPI.usage.searchApiKeys(
        filters.value.user_ids?.length === 1 ? filters.value.user_ids[0] : undefined,
        apiKeyKeyword.value || ''
      )
    } catch {
      apiKeyResults.value = []
    }
  }, 300)
}

const selectUser = async (u: SimpleUser) => {
  clearPendingUserSearch()
  userKeyword.value = ''
  showUserDropdown.value = false
  const userIds = Array.isArray(filters.value.user_ids) ? filters.value.user_ids : []
  if (!userIds.includes(u.id)) {
    filters.value.user_ids = [...userIds, u.id]
    selectedUsers.value = [...selectedUsers.value, u]
  }

  // 单选一个用户时预载其 API Key；多用户时搜索全部 Key。
  try {
    apiKeyResults.value = await adminAPI.usage.searchApiKeys(
      filters.value.user_ids.length === 1 ? u.id : undefined,
      ''
    )
  } catch {
    apiKeyResults.value = []
  }

  emitChange()
}

const clearUser = () => {
  clearPendingUserSearch()
  userKeyword.value = ''
  userResults.value = []
  showUserDropdown.value = false
  filters.value.user_ids = []
  selectedUsers.value = []
  emitChange()
}

const removeUser = (id: number) => {
  filters.value.user_ids = (filters.value.user_ids ?? []).filter((value: number) => value !== id)
  selectedUsers.value = selectedUsers.value.filter((user) => user.id !== id)
  emitChange()
}

const selectApiKey = (k: SimpleApiKey) => {
  apiKeyKeyword.value = ''
  showApiKeyDropdown.value = false
  const apiKeyIds = Array.isArray(filters.value.api_key_ids) ? filters.value.api_key_ids : []
  if (!apiKeyIds.includes(k.id)) {
    filters.value.api_key_ids = [...apiKeyIds, k.id]
    selectedApiKeys.value = [...selectedApiKeys.value, k]
  }
  emitChange()
}

const clearApiKey = () => {
  apiKeyKeyword.value = ''
  apiKeyResults.value = []
  showApiKeyDropdown.value = false
  filters.value.api_key_ids = []
  selectedApiKeys.value = []
}

const removeApiKey = (id: number) => {
  filters.value.api_key_ids = (filters.value.api_key_ids ?? []).filter((value: number) => value !== id)
  selectedApiKeys.value = selectedApiKeys.value.filter((key) => key.id !== id)
  emitChange()
}

const onClearApiKey = () => {
  clearApiKey()
  emitChange()
}

const debounceAccountSearch = () => {
  if (accountSearchTimeout) clearTimeout(accountSearchTimeout)
  accountSearchTimeout = setTimeout(async () => {
    if (!accountKeyword.value) {
      accountResults.value = []
      return
    }
    try {
      const res = await adminAPI.accounts.list(1, 20, { search: accountKeyword.value })
      accountResults.value = res.items.map((a) => ({ id: a.id, name: a.name }))
    } catch {
      accountResults.value = []
    }
  }, 300)
}

const selectAccount = (a: SimpleAccount) => {
  accountKeyword.value = ''
  showAccountDropdown.value = false
  const accountIds = Array.isArray(filters.value.account_ids) ? filters.value.account_ids : []
  if (!accountIds.includes(a.id)) {
    filters.value.account_ids = [...accountIds, a.id]
    selectedAccounts.value = [...selectedAccounts.value, a]
  }
  emitChange()
}

const clearAccount = () => {
  accountKeyword.value = ''
  accountResults.value = []
  showAccountDropdown.value = false
  filters.value.account_ids = []
  selectedAccounts.value = []
  emitChange()
}

const removeAccount = (id: number) => {
  filters.value.account_ids = (filters.value.account_ids ?? []).filter((value: number) => value !== id)
  selectedAccounts.value = selectedAccounts.value.filter((account) => account.id !== id)
  emitChange()
}

const selectUpstreamSites = (hosts: unknown) => {
  const selectedHosts = Array.isArray(hosts) ? hosts.filter((host): host is string => typeof host === 'string') : []
  const selectedHostSet = new Set(selectedHosts)
  const accountIDs = new Set<number>()
  for (const site of upstreamSites.value) {
    if (!selectedHostSet.has(site.host)) continue
    for (const accountID of site.account_ids) accountIDs.add(accountID)
  }
  filters.value.upstream_site_hosts = selectedHosts
  filters.value.upstream_site_account_ids = Array.from(accountIDs)
  emitChange()
}

const onApiKeyFocus = () => {
  showApiKeyDropdown.value = true
  // Trigger search if no results yet
  if (apiKeyResults.value.length === 0) {
    debounceApiKeySearch()
  }
}

const onDocumentClick = (e: MouseEvent) => {
  const target = e.target as Node | null
  if (!target) return

  const clickedInsideUser = userSearchRef.value?.contains(target) ?? false
  const clickedInsideApiKey = apiKeySearchRef.value?.contains(target) ?? false
  const clickedInsideAccount = accountSearchRef.value?.contains(target) ?? false

  if (!clickedInsideUser) showUserDropdown.value = false
  if (!clickedInsideApiKey) showApiKeyDropdown.value = false
  if (!clickedInsideAccount) showAccountDropdown.value = false
}

watch(
  () => props.startDate,
  (value) => {
    filters.value.start_date = value
  },
  { immediate: true }
)

watch(
  () => props.endDate,
  (value) => {
    filters.value.end_date = value
  },
  { immediate: true }
)

watch(
  () => filters.value.user_ids,
  (userIds) => {
    if (!userIds?.length) {
      clearPendingUserSearch()
      userKeyword.value = ''
      userResults.value = []
      selectedUsers.value = []
    }
  },
  { deep: true }
)

watch(
  () => filters.value.api_key_ids,
  (apiKeyIds) => {
    if (!apiKeyIds?.length) {
      apiKeyKeyword.value = ''
      apiKeyResults.value = []
      selectedApiKeys.value = []
    }
  },
  { deep: true }
)

watch(
  () => filters.value.account_ids,
  (accountIds) => {
    if (!accountIds?.length) {
      accountKeyword.value = ''
      accountResults.value = []
      selectedAccounts.value = []
    }
  },
  { deep: true }
)

onMounted(async () => {
  document.addEventListener('click', onDocumentClick)
  try {
    const [gs, sites] = await Promise.all([
      adminAPI.groups.list(1, 1000),
      adminAPI.accounts.listUpstreamSites(),
    ])
    groupOptions.value = gs.items.map((g: any) => ({ value: g.id, label: g.name }))
    upstreamSites.value = sites.map((site) => ({ host: site.host, account_ids: site.account_ids }))
  } catch {
    // Ignore filter option loading errors (page still usable)
  }
})

onUnmounted(() => {
  clearPendingUserSearch()
  document.removeEventListener('click', onDocumentClick)
})

// 供外部(如用户排行下钻)在程序化设置 user_id 后回显选中的用户邮箱
const setUserKeyword = (email: string) => {
  clearPendingUserSearch()
  userKeyword.value = email
  userResults.value = []
  showUserDropdown.value = false
}

const setSelectedUser = (id: number, email: string) => {
  filters.value.user_ids = [id]
  selectedUsers.value = [{ id, email, deleted: false }]
  setUserKeyword('')
}

const getUserSearchRevision = () => userSearchSequence

defineExpose({ getUserSearchRevision, setUserKeyword, setSelectedUser, selectUpstreamSites })
</script>
