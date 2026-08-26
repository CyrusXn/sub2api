import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UpstreamSitesView from '../UpstreamSitesView.vue'

const { listUpstreamSites, upsertUpstreamSiteCredential, showError, showSuccess, showInfo } = vi.hoisted(() => ({
  listUpstreamSites: vi.fn(),
  upsertUpstreamSiteCredential: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      listUpstreamSites,
      upsertUpstreamSiteCredential
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showInfo })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const DataTableStub = {
  props: ['columns', 'data', 'loading'],
  template: `
    <div>
      <div data-test="columns">{{ JSON.stringify(columns.map((column) => ({ key: column.key, width: column.width }))) }}</div>
      <div v-for="row in data" :key="row.host" data-test="site-row">
        <span>{{ row.website_url }}</span>
        <span>{{ row.account_names.join(',') }}</span>
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
}

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>'
}

const mountView = async () => {
  const wrapper = mount(UpstreamSitesView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
        DataTable: DataTableStub,
        BaseDialog: BaseDialogStub,
        Icon: true
      }
    }
  })
  await flushPromises()
  return wrapper
}

describe('UpstreamSitesView', () => {
  beforeEach(() => {
    vi.unstubAllEnvs()
    vi.clearAllMocks()
    listUpstreamSites.mockResolvedValue([
      {
        host: 'vovoapi.com',
        display_name: 'VoVo',
        website_url: 'https://vovoapi.com',
        account_ids: [12, 13],
        account_names: ['VoVo Plus', 'VoVo Backup'],
        login_username: 'admin@example.com',
        has_password: true,
        protocol: 'innom'
      }
    ])
    upsertUpstreamSiteCredential.mockResolvedValue({})
  })

  it('loads all upstream sites and keeps practical default column widths', async () => {
    const wrapper = await mountView()

    expect(listUpstreamSites).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('https://vovoapi.com')
    expect(wrapper.text()).toContain('VoVo Plus')
    expect(JSON.parse(wrapper.get('[data-test="columns"]').text())).toEqual([
      { key: 'display_name', width: 160 },
      { key: 'website_url', width: 240 },
      { key: 'account_names', width: 320 },
      { key: 'protocol', width: 110 },
      { key: 'login_username', width: 220 },
      { key: 'password_status', width: 120 },
      { key: 'actions', width: 120 }
    ])
  })

  it('edits a shared site credential without exposing the saved password', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-test="edit-site"]').trigger('click')
    expect(wrapper.get('[data-test="display-name"]').element).toHaveProperty('value', 'VoVo')
    expect(wrapper.get('[data-test="login-username"]').element).toHaveProperty('value', 'admin@example.com')
    expect(wrapper.get('[data-test="login-password"]').element).toHaveProperty('value', '')

    await wrapper.get('[data-test="login-username"]').setValue('updated@example.com')
    await wrapper.get('[data-test="login-password"]').setValue('replacement-value')
    await wrapper.get('[data-test="save-site"]').trigger('click')
    await flushPromises()

    expect(upsertUpstreamSiteCredential).toHaveBeenCalledWith({
      base_url: 'https://vovoapi.com',
      display_name: 'VoVo',
      login_username: 'updated@example.com',
      login_password: 'replacement-value'
    })
    expect(listUpstreamSites).toHaveBeenCalledTimes(2)
    expect(showSuccess).toHaveBeenCalled()
  })

  it('does not write upstream credentials in read-only preview mode', async () => {
    vi.stubEnv('VITE_READ_ONLY_PREVIEW', 'true')
    const wrapper = await mountView()

    await wrapper.get('[data-test="edit-site"]').trigger('click')
    await wrapper.get('[data-test="login-password"]').setValue('preview-only-value')
    await wrapper.get('[data-test="save-site"]').trigger('click')
    await flushPromises()

    expect(upsertUpstreamSiteCredential).not.toHaveBeenCalled()
    expect(showInfo).toHaveBeenCalledWith('admin.accounts.upstreamSites.readOnlyPreview')
  })
})
