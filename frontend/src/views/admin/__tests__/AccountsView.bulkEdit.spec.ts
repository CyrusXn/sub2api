import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  probeUpstreamBilling,
  probeUpstreamBillingBatch,
  showError,
  showSuccess
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  probeUpstreamBilling: vi.fn(),
  probeUpstreamBillingBatch: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      probeUpstreamBilling,
      probeUpstreamBillingBatch,
      toggleSchedulable: vi.fn()
    },
    proxies: {
      getAll: getAllProxies
    },
    groups: {
      getAll: getAllGroups
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    token: 'test-token'
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const DataTableStub = {
  props: [
    'columns',
    'data',
    'defaultSortKey',
    'defaultSortOrder',
    'columnWidthStorageKey',
    'columnOrderStorageKey',
    'selectable',
    'selectedKeys'
  ],
  emits: ['selectionChange'],
  template: `
    <div data-test="data-table">
      <span v-for="column in columns" :key="column.key" data-test="column-key">{{ column.key }}</span>
      <div v-for="row in data" :key="row.id">
        <input
          v-if="selectable"
          data-test="select-row"
          type="checkbox"
          :checked="selectedKeys.includes(row.id)"
          @change="$emit('selectionChange', selectedKeys.includes(row.id) ? selectedKeys.filter(id => id !== row.id) : [...selectedKeys, row.id])"
        />
        <slot name="cell-created_at" :value="row.created_at" :row="row" />
        <slot name="cell-actions" :row="row" />
        <div data-test="account-rate"><slot name="cell-rate_multiplier" :row="row" /></div>
      </div>
    </div>
  `
}

const ProbeDataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <div data-test="account-rate"><slot name="cell-rate_multiplier" :row="row" /></div>
        <slot name="cell-upstream_billing_rate" :row="row" />
      </div>
    </div>
  `
}

const AccountBulkActionsBarStub = {
  props: ['selectedIds'],
  emits: ['edit-filtered', 'probe-upstream-billing'],
  template: `
    <div>
      <button data-test="edit-filtered" @click="$emit('edit-filtered')">edit filtered</button>
      <button data-test="probe-upstream-billing" @click="$emit('probe-upstream-billing')">probe</button>
    </div>
  `
}

const AccountTableActionsRefreshStub = {
  emits: ['refresh'],
  template: '<button data-test="manual-refresh" @click="$emit(\'refresh\')">refresh</button>'
}

const PaginationStub = {
  emits: ['update:page'],
  template: '<button data-test="next-page" @click="$emit(\'update:page\', 2)">next</button>'
}

const BulkEditAccountModalStub = {
  props: ['show', 'target'],
  template: '<div data-test="bulk-edit-modal" :data-show="String(show)" :data-target-mode="target?.mode ?? \'\'"></div>'
}

const expiredProbeSnapshot = () => ({
  status: 'ok',
  data: {
    object: 'sub2api.key_billing',
    schema_version: 1,
    billing_scope: 'token',
    group_rate_multiplier: 0.5,
    resolved_rate_multiplier: 0.5,
    peak_rate_enabled: false,
    effective_rate_multiplier: 0.5,
    observed_at: new Date(Date.now() - 120_000).toISOString()
  },
  fresh_until: new Date(Date.now() - 60_000).toISOString(),
  last_attempt_at: new Date(Date.now() - 120_000).toISOString(),
  next_probe_at: new Date(Date.now() - 30_000).toISOString()
})

const dueProbeAccount = (id: number, overrides: Record<string, unknown> = {}) => ({
  id,
  name: `upstream-${id}`,
  platform: 'openai',
  type: 'apikey',
  status: 'active',
  schedulable: true,
  extra: {
    upstream_billing_probe_enabled: true,
    upstream_billing_probe: expiredProbeSnapshot()
  },
  created_at: '2026-07-13T00:00:00Z',
  updated_at: '2026-07-13T00:00:00Z',
  ...overrides
})

const mountAccountsForSortAndProbe = () => mount(AccountsView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: { template: '<div><slot name="table" /></div>' },
      DataTable: DataTableStub,
      AccountTableActions: true,
      AccountTableFilters: true,
      AccountBulkActionsBar: true,
      AccountActionMenu: true,
      Pagination: true,
      ConfirmDialog: true,
      ImportDataModal: true,
      ReAuthAccountModal: true,
      AccountTestModal: true,
      AccountStatsModal: true,
      ScheduledTestsPanel: true,
      SyncFromCrsModal: true,
      TempUnschedStatusModal: true,
      ErrorPassthroughRulesModal: true,
      TLSFingerprintProfilesModal: true,
      CreateAccountModal: true,
      EditAccountModal: true,
      BulkEditAccountModal: true,
      PlatformTypeBadge: true,
      AccountCapacityCell: true,
      AccountStatusIndicator: true,
      AccountTodayStatsCell: true,
      AccountGroupsCell: true,
      AccountUsageCell: true,
      Icon: true
    }
  }
})

describe('admin AccountsView bulk edit scope', () => {
  beforeEach(() => {
    vi.unstubAllEnvs()
    localStorage.clear()

    listAccounts.mockReset()
    listWithEtag.mockReset()
    getBatchTodayStats.mockReset()
    getUpstreamBillingProbeSettings.mockReset()
    getAllProxies.mockReset()
    getAllGroups.mockReset()
    probeUpstreamBilling.mockReset()
    probeUpstreamBillingBatch.mockReset()
    showError.mockReset()
    showSuccess.mockReset()

    listAccounts.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
      pages: 0
    })
    listWithEtag.mockResolvedValue({
      notModified: true,
      etag: null,
      data: null
    })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: true, interval_minutes: 30 })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
    probeUpstreamBilling.mockResolvedValue({})
    probeUpstreamBillingBatch.mockResolvedValue([])
  })

  it('defaults the account list to upstream billing rate ascending', async () => {
    const wrapper = mountAccountsForSortAndProbe()

    await flushPromises()

    expect(listAccounts).toHaveBeenCalledWith(
      expect.any(Number),
      expect.any(Number),
      expect.objectContaining({ sort_by: 'upstream_billing_rate', sort_order: 'asc' }),
      expect.any(Object)
    )
    const table = wrapper.getComponent(DataTableStub)
    expect(table.props('defaultSortKey')).toBe('upstream_billing_rate')
    expect(table.props('defaultSortOrder')).toBe('asc')
  })

  it('exposes the account settlement multiplier as an admin-only account column', async () => {
    const wrapper = mountAccountsForSortAndProbe()

    await flushPromises()

    const columnKeys = wrapper.findAll('[data-test="column-key"]').map(node => node.text())
    expect(columnKeys).toContain('admin_usage_multiplier')
  })

  it('migrates the previous name ascending default to upstream billing rate ascending once', async () => {
    localStorage.setItem('account-table-sort', JSON.stringify({ key: 'name', order: 'asc' }))

    mountAccountsForSortAndProbe()
    await flushPromises()

    expect(listAccounts).toHaveBeenCalledWith(
      expect.any(Number),
      expect.any(Number),
      expect.objectContaining({ sort_by: 'upstream_billing_rate', sort_order: 'asc' }),
      expect.any(Object)
    )
    expect(localStorage.getItem('account-table-sort')).toBe(JSON.stringify({
      key: 'upstream_billing_rate',
      order: 'asc'
    }))
    expect(localStorage.getItem('account-table-sort-default-version')).toBe('upstream-billing-rate-asc')
  })

  it('preserves an administrator selected account sort during the default migration', async () => {
    localStorage.setItem('account-table-sort', JSON.stringify({ key: 'created_at', order: 'desc' }))

    mountAccountsForSortAndProbe()
    await flushPromises()

    expect(listAccounts).toHaveBeenCalledWith(
      expect.any(Number),
      expect.any(Number),
      expect.objectContaining({ sort_by: 'created_at', sort_order: 'desc' }),
      expect.any(Object)
    )
    expect(localStorage.getItem('account-table-sort')).toBe(JSON.stringify({ key: 'created_at', order: 'desc' }))
  })

  it('opens bulk edit in filtered-results mode from the bulk actions dropdown', async () => {
    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          Pagination: true,
          ConfirmDialog: true,
          AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
          AccountTableFilters: { template: '<div></div>' },
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    await wrapper.get('[data-test="edit-filtered"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="bulk-edit-modal"]').attributes('data-show')).toBe('true')
    expect(wrapper.get('[data-test="bulk-edit-modal"]').attributes('data-target-mode')).toBe('filtered')
  })

  it('renders the created_at column with compact selection and persisted table layout', async () => {
    listAccounts.mockResolvedValue({
      items: [
        {
          id: 1,
          name: 'test-account',
          platform: 'anthropic',
          type: 'apikey',
          status: 'active',
          schedulable: true,
          created_at: '2026-03-07T10:00:00Z',
          updated_at: '2026-03-07T10:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          Pagination: true,
          ConfirmDialog: true,
          AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
          AccountTableFilters: { template: '<div></div>' },
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()

    const columnKeys = wrapper.findAll('[data-test="column-key"]').map(node => node.text())
    expect(columnKeys).toContain('created_at')
    const table = wrapper.getComponent(DataTableStub)
    const columns = table.props('columns') as Array<{ key: string; label: string; sortable: boolean; width?: number }>
    expect(columns.find(column => column.key === 'created_at')).toMatchObject({
      label: 'admin.accounts.columns.createdAt',
      sortable: true
    })
    expect(columns.some(column => column.key === 'select')).toBe(false)
    expect(columns.find(column => column.key === 'actions')?.width).toBe(340)
    expect(table.props('selectable')).toBe(true)
    expect(table.props('selectedKeys')).toEqual([])
    expect(wrapper.get('[data-test="account-action-test"]').text()).toBe('admin.accounts.testConnection')
    expect(wrapper.get('[data-test="account-action-duplicate"]').text()).toBe('admin.accounts.duplicateAccount')
    expect(wrapper.get('[data-test="account-action-stats"]').text()).toBe('admin.accounts.viewStats')
    const schedulableIndex = columns.findIndex(column => column.key === 'schedulable')
    expect(schedulableIndex).toBeGreaterThanOrEqual(0)
    expect(columns[schedulableIndex + 1]?.key).toBe('upstream_billing_rate')
    expect(table.props('columnWidthStorageKey')).toBe('account-table-column-widths:v2')
    expect(table.props('columnOrderStorageKey')).toBe('account-table-column-order:v2')
  })

  it('migrates old persisted column order to keep upstream billing rate next to schedulable', async () => {
    localStorage.setItem(
      'account-table-column-order:v2',
      JSON.stringify(['name', 'schedulable', 'priority', 'upstream_billing_rate', 'created_at'])
    )

    mountAccountsForSortAndProbe()
    await flushPromises()

    expect(JSON.parse(localStorage.getItem('account-table-column-order:v2') || '[]')).toEqual([
      'name',
      'schedulable',
      'upstream_billing_rate',
      'priority',
      'created_at'
    ])
    expect(localStorage.getItem('account-table-column-order-version')).toBe('upstream-rate-after-schedulable')
  })

  it('passes the loaded global probe state to every upstream billing cell', async () => {
    listAccounts.mockResolvedValue({
      items: [
        {
          id: 1,
          name: 'upstream',
          platform: 'openai',
          type: 'apikey',
          status: 'active',
          schedulable: true,
          created_at: '2026-07-13T00:00:00Z',
          updated_at: '2026-07-13T00:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: false, interval_minutes: 30 })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /></div>' },
          DataTable: {
            props: ['data'],
            template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-upstream_billing_rate" :row="row" /></div></div>'
          },
          UpstreamBillingRateCell: {
            props: ['globalProbeEnabled'],
            template: '<span data-test="upstream-billing-cell" :data-global-enabled="String(globalProbeEnabled)"></span>'
          },
          Pagination: true,
          ConfirmDialog: true,
          AccountTableActions: true,
          AccountTableFilters: true,
          AccountBulkActionsBar: true,
          AccountActionMenu: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: true,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()

    expect(getUpstreamBillingProbeSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="upstream-billing-cell"]').attributes('data-global-enabled')).toBe('false')
  })

  it('refreshes visible upstream rates in batches of 20 after the account query', async () => {
    const accounts = Array.from({ length: 21 }, (_, index) => dueProbeAccount(index + 1))
    listAccounts.mockResolvedValue({ items: accounts, total: 21, page: 1, page_size: 21, pages: 1 })

    mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /></div>' },
          DataTable: DataTableStub,
          AccountTableActions: AccountTableActionsRefreshStub,
          AccountTableFilters: true,
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          Pagination: true,
          ConfirmDialog: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()

    expect(probeUpstreamBillingBatch).toHaveBeenNthCalledWith(1, accounts.slice(0, 20).map(account => account.id))
    expect(probeUpstreamBillingBatch).toHaveBeenNthCalledWith(2, [21])
  })

  it('auto-probes enabled API-key accounts and expired persisted snapshots', async () => {
    const future = new Date(Date.now() + 60_000).toISOString()
    const accounts = [
      dueProbeAccount(1),
      dueProbeAccount(2, { extra: { upstream_billing_probe_enabled: false, upstream_billing_probe: expiredProbeSnapshot() } }),
      dueProbeAccount(3, { extra: { upstream_billing_probe_enabled: true, upstream_billing_probe: { ...expiredProbeSnapshot(), fresh_until: future } } }),
      dueProbeAccount(4, { extra: { upstream_billing_probe_enabled: true, upstream_billing_probe: { ...expiredProbeSnapshot(), next_probe_at: future } } }),
      dueProbeAccount(5, { type: 'oauth' }),
      dueProbeAccount(6, { extra: { upstream_billing_probe_enabled: true } })
    ]
    listAccounts.mockResolvedValue({ items: accounts, total: accounts.length, page: 1, page_size: 20, pages: 1 })

    mountAccountsForSortAndProbe()
    await flushPromises()

    expect(probeUpstreamBillingBatch).toHaveBeenCalledTimes(1)
    expect(probeUpstreamBillingBatch).toHaveBeenCalledWith([1, 2, 6])
  })

  it('forces a fresh upstream billing probe for visible accounts on manual refresh', async () => {
    const future = new Date(Date.now() + 60_000).toISOString()
    const account = dueProbeAccount(9, {
      extra: {
        upstream_billing_probe_enabled: false,
        upstream_billing_probe: {
          ...expiredProbeSnapshot(),
          fresh_until: future,
          next_probe_at: future
        }
      }
    })
    listAccounts.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
          DataTable: DataTableStub,
          AccountTableActions: AccountTableActionsRefreshStub,
          AccountTableFilters: true,
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          Pagination: true,
          ConfirmDialog: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    expect(probeUpstreamBillingBatch).not.toHaveBeenCalled()

    await wrapper.get('[data-test="manual-refresh"]').trigger('click')
    await flushPromises()

    expect(probeUpstreamBillingBatch).toHaveBeenCalledTimes(1)
    expect(probeUpstreamBillingBatch).toHaveBeenCalledWith([9])
  })

  it('does not auto-probe accounts while the global probe switch is disabled', async () => {
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: false, interval_minutes: 30 })
    listAccounts.mockResolvedValue({ items: [dueProbeAccount(1)], total: 1, page: 1, page_size: 20, pages: 1 })

    mountAccountsForSortAndProbe()
    await flushPromises()

    expect(probeUpstreamBillingBatch).not.toHaveBeenCalled()
  })

  it('does not probe upstream accounts when the local preview is read-only', async () => {
    vi.stubEnv('VITE_READ_ONLY_PREVIEW', 'true')
    listAccounts.mockResolvedValue({
      items: [{
        id: 1,
        name: 'upstream',
        platform: 'openai',
        type: 'apikey',
        status: 'active',
        schedulable: true,
        created_at: '2026-07-13T00:00:00Z',
        updated_at: '2026-07-13T00:00:00Z'
      }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /></div>' },
          DataTable: DataTableStub,
          AccountTableActions: true,
          AccountTableFilters: true,
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          Pagination: true,
          ConfirmDialog: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()

    expect(probeUpstreamBillingBatch).not.toHaveBeenCalled()
    await wrapper.get('[data-test="select-row"]').trigger('change')
    await wrapper.get('[data-test="probe-upstream-billing"]').trigger('click')
    await flushPromises()
    expect(probeUpstreamBillingBatch).not.toHaveBeenCalled()
  })

  it('refreshes upstream rates for the new page after pagination reloads the account query', async () => {
    const account = (id: number) => dueProbeAccount(id)
    listAccounts
      .mockResolvedValueOnce({ items: [account(1)], total: 2, page: 1, page_size: 1, pages: 2 })
      .mockResolvedValueOnce({ items: [account(2)], total: 2, page: 2, page_size: 1, pages: 2 })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /><slot name="pagination" /></div>' },
          DataTable: DataTableStub,
          Pagination: PaginationStub,
          ConfirmDialog: true,
          AccountTableActions: true,
          AccountTableFilters: true,
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    probeUpstreamBillingBatch.mockClear()

    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()

    expect(probeUpstreamBillingBatch).toHaveBeenCalledTimes(1)
    expect(probeUpstreamBillingBatch).toHaveBeenCalledWith([2])
  })

  it('submits selected account IDs from every page for backend eligibility checks', async () => {
    const account = (id: number) => ({
      id,
      name: `account-${id}`,
      platform: 'openai',
      type: 'apikey',
      status: 'active',
      schedulable: true,
      created_at: '2026-07-13T00:00:00Z',
      updated_at: '2026-07-13T00:00:00Z'
    })
    listAccounts
      .mockResolvedValueOnce({ items: [account(7)], total: 2, page: 1, page_size: 1, pages: 2 })
      .mockResolvedValueOnce({ items: [account(11)], total: 2, page: 2, page_size: 1, pages: 2 })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /><slot name="pagination" /></div>' },
          DataTable: DataTableStub,
          Pagination: PaginationStub,
          ConfirmDialog: true,
          AccountTableActions: true,
          AccountTableFilters: true,
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountActionMenu: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    await wrapper.get('[data-test="select-row"]').trigger('change')
    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="select-row"]').trigger('change')
    await wrapper.get('[data-test="probe-upstream-billing"]').trigger('click')
    await flushPromises()

    expect(probeUpstreamBillingBatch).toHaveBeenCalledWith([7, 11])
  })

  it('refreshes the current page after a batch probe and displays the synced rate', async () => {
    const account = (id: number, rateMultiplier: number) => ({
      id,
      name: `account-${id}`,
      platform: 'openai',
      type: 'apikey',
      status: 'active',
      schedulable: true,
      rate_multiplier: rateMultiplier,
      created_at: '2026-07-13T00:00:00Z',
      updated_at: '2026-07-13T00:00:00Z'
    })
    listAccounts
      .mockResolvedValueOnce({ items: [account(7, 0.25)], total: 2, page: 1, page_size: 1, pages: 2 })
      .mockResolvedValueOnce({ items: [account(11, 0.25)], total: 2, page: 2, page_size: 1, pages: 2 })
      .mockResolvedValueOnce({ items: [account(11, 0.065)], total: 2, page: 2, page_size: 1, pages: 2 })
    probeUpstreamBillingBatch.mockResolvedValue([
      {
        account_id: 11,
        snapshot: {
          status: 'ok',
          data: { effective_rate_multiplier: 0.065 },
          last_attempt_at: '2026-07-13T00:00:00Z',
          next_probe_at: '2026-07-13T00:30:00Z'
        }
      }
    ])

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /><slot name="pagination" /></div>' },
          DataTable: DataTableStub,
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountTableActions: true,
          AccountTableFilters: true,
          AccountActionMenu: true,
          Pagination: PaginationStub,
          ConfirmDialog: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="select-row"]').trigger('change')
    await wrapper.get('[data-test="probe-upstream-billing"]').trigger('click')
    await flushPromises()

    expect(probeUpstreamBillingBatch).toHaveBeenCalledWith([11])
    expect(listAccounts).toHaveBeenCalledTimes(3)
    expect(listAccounts.mock.calls[2]?.[0]).toBe(2)
    expect(wrapper.get('[data-test="account-rate"]').text()).toBe('0.065x')
  })

  it('does not report a successful batch probe as failed when the list refresh fails', async () => {
    const account = {
      id: 7,
      name: 'account-7',
      platform: 'openai',
      type: 'apikey',
      status: 'active',
      schedulable: true,
      rate_multiplier: 0.25,
      created_at: '2026-07-13T00:00:00Z',
      updated_at: '2026-07-13T00:00:00Z'
    }
    listAccounts
      .mockResolvedValueOnce({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
      .mockRejectedValueOnce(new Error('refresh failed'))
    probeUpstreamBillingBatch.mockResolvedValue([
      {
        account_id: 7,
        snapshot: {
          status: 'ok',
          data: { effective_rate_multiplier: 0.065 },
          last_attempt_at: '2026-07-13T00:00:00Z',
          next_probe_at: '2026-07-13T00:30:00Z'
        }
      }
    ])
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /></div>' },
          DataTable: DataTableStub,
          AccountBulkActionsBar: AccountBulkActionsBarStub,
          AccountTableActions: true,
          AccountTableFilters: true,
          AccountActionMenu: true,
          Pagination: true,
          ConfirmDialog: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    await wrapper.get('[data-test="select-row"]').trigger('change')
    const listCallsBeforeProbe = listAccounts.mock.calls.length
    await wrapper.get('[data-test="probe-upstream-billing"]').trigger('click')
    await flushPromises()

    expect(probeUpstreamBillingBatch).toHaveBeenCalledWith([7])
    expect(listAccounts).toHaveBeenCalledTimes(listCallsBeforeProbe + 1)
    expect(showError).not.toHaveBeenCalled()
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.upstreamBilling.batchCompleted')
    consoleError.mockRestore()
  })

  it('refreshes the account row after a successful single-account probe', async () => {
    const account = (rateMultiplier: number) => ({
      id: 7,
      name: 'account-7',
      platform: 'openai',
      type: 'apikey',
      status: 'active',
      schedulable: true,
      rate_multiplier: rateMultiplier,
      extra: { upstream_billing_probe_enabled: true },
      created_at: '2026-07-13T00:00:00Z',
      updated_at: '2026-07-13T00:00:00Z'
    })
    listAccounts
      .mockResolvedValueOnce({ items: [account(0.25)], total: 1, page: 1, page_size: 20, pages: 1 })
      .mockResolvedValueOnce({ items: [account(0.065)], total: 1, page: 1, page_size: 20, pages: 1 })
    probeUpstreamBilling.mockResolvedValue({
      account_id: 7,
      snapshot: {
        status: 'ok',
        data: { effective_rate_multiplier: 0.065 },
        last_attempt_at: '2026-07-13T00:00:00Z',
        next_probe_at: '2026-07-13T00:30:00Z'
      }
    })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /></div>' },
          DataTable: ProbeDataTableStub,
          AccountBulkActionsBar: true,
          AccountTableActions: true,
          AccountTableFilters: true,
          AccountActionMenu: true,
          Pagination: true,
          ConfirmDialog: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: true,
          PlatformTypeBadge: true,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    await wrapper.get('[data-testid="upstream-billing-probe"]').trigger('click')
    await flushPromises()

    expect(probeUpstreamBilling).toHaveBeenCalledWith(7)
    expect(listAccounts).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="account-rate"]').text()).toBe('0.065x')
  })
})
