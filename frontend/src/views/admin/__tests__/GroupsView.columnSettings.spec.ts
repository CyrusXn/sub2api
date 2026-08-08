import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { AdminGroup } from '@/types'
import GroupsView from '../GroupsView.vue'

const {
  listGroups,
  getAllGroups,
  getModelsListCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  listAccounts,
  createGroupRequest,
  showError,
  showSuccess,
  isCurrentStep,
  nextStep,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getAllGroups: vi.fn(),
  getModelsListCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  listAccounts: vi.fn(),
  createGroupRequest: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
}))

const messages: Record<string, string> = {
  'admin.groups.columnSettings': 'Column Settings',
  'admin.groups.columns.name': 'Name',
  'admin.groups.columns.id': 'ID',
  'admin.groups.columns.platform': 'Platform',
  'admin.groups.columns.sortOrder': 'Sort Value',
  'admin.groups.columns.billingType': 'Billing Type',
  'admin.groups.columns.rateMultiplier': 'Rate Multiplier',
  'admin.groups.columns.adminUsageMultiplier': 'Admin Usage Multiplier',
  'admin.groups.columns.type': 'Type',
  'admin.groups.columns.accounts': 'Accounts',
  'admin.groups.columns.capacity': 'Capacity',
  'admin.groups.columns.usage': 'Usage',
  'admin.groups.columns.status': 'Status',
  'admin.groups.columns.actions': 'Actions',
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: getAllGroups,
      getModelsListCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      create: createGroupRequest,
      update: vi.fn(),
      delete: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    accounts: {
      list: listAccounts,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep,
    nextStep,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const createGroup = (overrides: Partial<AdminGroup> = {}): AdminGroup => ({
  id: 1,
  name: 'Core Anthropic',
  description: null,
  platform: 'anthropic',
  rate_multiplier: 1,
  admin_usage_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  default_mapped_model: '',
  messages_dispatch_model_config: undefined,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  account_count: 3,
  active_account_count: 2,
  rate_limited_account_count: 1,
  models_list_config: undefined,
  sort_order: 10,
  ...overrides,
})

const AppLayoutStub = {
  template: '<div><slot /></div>',
}

const TablePageLayoutStub = {
  template: `
    <div>
      <slot name="filters" />
      <slot name="table" />
      <slot name="pagination" />
    </div>
  `,
}

const DataTableStub = {
  props: [
    'columns',
    'data',
    'defaultSortKey',
    'defaultSortOrder',
    'columnWidthStorageKey',
    'columnOrderStorageKey',
  ],
  emits: ['sort'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="columns-meta">{{ JSON.stringify(columns.map((col) => ({ key: col.key, width: col.width }))) }}</div>
      <div data-test="rows">{{ data.map((row) => row.name).join(',') }}</div>
    </div>
  `,
}

const SelectStub = {
  props: ['modelValue', 'options', 'placeholder'],
  emits: ['update:modelValue', 'change'],
  template: `
    <select
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value); $emit('change')"
    >
      <option v-for="option in options" :key="String(option.value)" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `,
}

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const VueDraggableStub = {
  props: ['modelValue', 'move'],
  template: '<div><slot /></div>',
}

const mountView = async () => {
  const wrapper = mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        PlatformIcon: true,
        Icon: IconStub,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: VueDraggableStub,
      },
    },
  })
  await flushPromises()
  return wrapper
}

const columnKeys = (wrapper: ReturnType<typeof mount>) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

const openColumnSettings = async (wrapper: ReturnType<typeof mount>) => {
  await wrapper.get('button[title="Column Settings"]').trigger('click')
}

const clickColumnToggle = async (wrapper: ReturnType<typeof mount>, label: string) => {
  const button = wrapper
    .findAll('button')
    .find((item) => item.text().trim().startsWith(label))
  expect(button, `column toggle ${label}`).toBeTruthy()
  await button!.trigger('click')
  await flushPromises()
}

describe('admin GroupsView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listGroups.mockReset()
    getAllGroups.mockReset()
    getModelsListCandidates.mockReset()
    getUsageSummary.mockReset()
    getCapacitySummary.mockReset()
    getLiveCapability.mockReset()
    listAccounts.mockReset()
    createGroupRequest.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    listGroups.mockResolvedValue({
      items: [createGroup()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAllGroups.mockResolvedValue([])
    getModelsListCandidates.mockResolvedValue([])
    getUsageSummary.mockResolvedValue([])
    getCapacitySummary.mockResolvedValue([])
    getLiveCapability.mockResolvedValue({ supported: false })
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    createGroupRequest.mockResolvedValue(createGroup())
    isCurrentStep.mockReturnValue(false)
  })

  it('starts with an empty search and disables browser autofill on the search input', async () => {
    const wrapper = await mountView()
    const search = wrapper.get('input[type="text"]')

    expect(search.element.value).toBe('')
    expect(search.attributes('autocomplete')).toBe('off')
    expect(search.attributes('autocorrect')).toBe('off')
    expect(search.attributes('autocapitalize')).toBe('none')
    expect(search.attributes('spellcheck')).toBe('false')
    expect(listGroups).toHaveBeenCalledWith(
      1,
      20,
      expect.objectContaining({ search: undefined }),
      expect.any(Object)
    )
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('hides the id column by default while keeping other group columns visible', async () => {
    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'platform',
      'sort_order',
      'billing_type',
      'rate_multiplier',
      'admin_usage_multiplier',
      'is_exclusive',
      'account_count',
      'capacity',
      'usage',
      'status',
      'actions',
    ])
    const columnMeta = JSON.parse(wrapper.get('[data-test="columns-meta"]').text()) as Array<{ key: string; width?: number }>
    expect(columnMeta.find((column) => column.key === 'actions')?.width).toBe(300)
    expect(columnMeta.find((column) => column.key === 'sort_order')).toMatchObject({
      key: 'sort_order',
    })

    const table = wrapper.getComponent(DataTableStub)
    expect(table.props('defaultSortKey')).toBe('platform')
    expect(table.props('defaultSortOrder')).toBe('asc')
    expect(table.props('columnWidthStorageKey')).toBe('group-table-column-widths:v2')
    expect(table.props('columnOrderStorageKey')).toBe('group-table-column-order')
    expect(localStorage.getItem('group-hidden-columns')).toBe(JSON.stringify(['id']))
    expect(localStorage.getItem('group-column-settings-version')).toBe('2')
  })

  it('requests the platform-priority default sort and exposes the sort value as sortable', async () => {
    const wrapper = await mountView()

    expect(listGroups).toHaveBeenCalledWith(
      1,
      20,
      expect.objectContaining({ sort_by: 'platform', sort_order: 'asc' }),
      expect.any(Object),
    )
    const columns = wrapper.getComponent(DataTableStub).props('columns') as Array<{
      key: string
      sortable: boolean
    }>
    expect(columns.find((column) => column.key === 'sort_order')).toMatchObject({ sortable: true })
  })

  it('only allows drag sorting within the same platform', async () => {
    const anthropic = createGroup({ id: 1, platform: 'anthropic' })
    const anotherAnthropic = createGroup({ id: 2, platform: 'anthropic' })
    const openai = createGroup({ id: 3, platform: 'openai' })
    getAllGroups.mockResolvedValue([anthropic, anotherAnthropic, openai])
    const wrapper = await mountView()

    await wrapper.get('button[title="admin.groups.sortOrder"]').trigger('click')
    await flushPromises()

    const canMove = wrapper.getComponent(VueDraggableStub).props('move') as (event: {
      draggedContext: { element: AdminGroup }
      relatedContext: { element: AdminGroup }
    }) => boolean
    expect(canMove({
      draggedContext: { element: anthropic },
      relatedContext: { element: anotherAnthropic },
    })).toBe(true)
    expect(canMove({
      draggedContext: { element: anthropic },
      relatedContext: { element: openai },
    })).toBe(false)
  })

  it('applies saved hidden columns on mount and ignores unknown keys', async () => {
    localStorage.setItem(
      'group-hidden-columns',
      JSON.stringify(['usage', 'capacity', 'removed_column', 'name', 'actions']),
    )
    localStorage.setItem('group-column-settings-version', '2')

    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'id',
      'platform',
      'sort_order',
      'billing_type',
      'rate_multiplier',
      'admin_usage_multiplier',
      'is_exclusive',
      'account_count',
      'status',
      'actions',
    ])
  })

  it('auto-hides id for existing saved column prefs after version bump', async () => {
    localStorage.setItem('group-hidden-columns', JSON.stringify(['usage']))
    // No version key → treated as version 1, migrate to 2 and hide id.

    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'platform',
      'sort_order',
      'billing_type',
      'rate_multiplier',
      'admin_usage_multiplier',
      'is_exclusive',
      'account_count',
      'capacity',
      'status',
      'actions',
    ])
    expect(JSON.parse(localStorage.getItem('group-hidden-columns')!)).toEqual(
      expect.arrayContaining(['usage', 'id']),
    )
    expect(localStorage.getItem('group-column-settings-version')).toBe('2')
  })

  it('toggles a column and persists hidden column keys', async () => {
    const wrapper = await mountView()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'Usage')

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'platform',
      'sort_order',
      'billing_type',
      'rate_multiplier',
      'admin_usage_multiplier',
      'is_exclusive',
      'account_count',
      'capacity',
      'status',
      'actions',
    ])
    expect(JSON.parse(localStorage.getItem('group-hidden-columns')!)).toEqual(
      expect.arrayContaining(['id', 'usage']),
    )
  })

  it('can show the id column from column settings', async () => {
    const wrapper = await mountView()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'ID')

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'id',
      'platform',
      'sort_order',
      'billing_type',
      'rate_multiplier',
      'admin_usage_multiplier',
      'is_exclusive',
      'account_count',
      'capacity',
      'usage',
      'status',
      'actions',
    ])
    expect(localStorage.getItem('group-hidden-columns')).toBe(JSON.stringify([]))
  })

  it('skips usage and capacity fetches until consuming columns are shown', async () => {
    localStorage.setItem(
      'group-hidden-columns',
      JSON.stringify(['billing_type', 'usage', 'capacity']),
    )

    const wrapper = await mountView()

    expect(getUsageSummary).not.toHaveBeenCalled()
    expect(getCapacitySummary).not.toHaveBeenCalled()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'Usage')
    expect(getUsageSummary).toHaveBeenCalledTimes(1)
    expect(getCapacitySummary).not.toHaveBeenCalled()

    await clickColumnToggle(wrapper, 'Capacity')
    expect(getUsageSummary).toHaveBeenCalledTimes(1)
    expect(getCapacitySummary).toHaveBeenCalledTimes(1)
  })

  it('submits an explicit zero admin usage multiplier when creating a group', async () => {
    const wrapper = await mountView()
    const createButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.groups.createGroup')
    )
    expect(createButton).toBeTruthy()
    await createButton!.trigger('click')

    await wrapper.get('[data-tour="group-form-name"]').setValue('Zero multiplier')
    await wrapper.get('[data-test="group-admin-usage-multiplier"]').setValue('0')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()

    expect(createGroupRequest).toHaveBeenCalledWith(expect.objectContaining({
      admin_usage_multiplier: 0
    }))
  })
})
