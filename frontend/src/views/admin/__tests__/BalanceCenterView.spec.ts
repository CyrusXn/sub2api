import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BalanceCenterView from '../BalanceCenterView.vue'

const api = vi.hoisted(() => ({
  getSettings: vi.fn(), sites: vi.fn(), overview: vi.fn(), updateSettings: vi.fn(), probeAccounts: vi.fn(),
  snapshots: vi.fn(), manualRows: vi.fn(), replaceManualRows: vi.fn(), rechargeEvents: vi.fn(),
  reconciliations: vi.fn(), alerts: vi.fn(), createRechargeEvent: vi.fn(), deleteRechargeEvent: vi.fn(),
  createReconciliation: vi.fn(), syncAutomaticRecords: vi.fn(), saveLiandongSession: vi.fn(), syncLiandong: vi.fn()
}))
const notifications = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { balanceCenter: api } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => ({ ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')), useI18n: () => ({ t: (key: string) => key }) }))

const mountView = () => mount(BalanceCenterView, { global: { stubs: {
  AppLayout: { template: '<div><slot /></div>' }, DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.account_id">{{ row.site_name }}</div></div>' },
  Pagination: true, Toggle: { props: ['modelValue'], emits: ['update:modelValue'], template: '<button class="toggle" />' }, Icon: true
} } })

describe('BalanceCenterView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.getSettings.mockResolvedValue({ enabled: false, event_probe_enabled: false, email_enabled: false, low_balance_threshold: 5 })
    api.sites.mockResolvedValue([])
    api.overview.mockResolvedValue([{ account_id: 7, site_name: 'VoVo', normalized_domain: 'vovoapi.com', account_name: 'VoVo', status: 'ok' }])
    api.updateSettings.mockResolvedValue({})
    api.probeAccounts.mockResolvedValue([])
  })

  it('loads overview and saves the disabled-by-default settings', async () => {
    const wrapper = mountView(); await flushPromises()
    expect(api.overview).toHaveBeenCalledOnce(); expect(wrapper.text()).toContain('VoVo')
    await wrapper.get('[data-test="save-settings"]').trigger('click'); await flushPromises()
    expect(api.updateSettings).toHaveBeenCalledWith({ enabled: false, event_probe_enabled: false, email_enabled: false, low_balance_threshold: 5 })
  })

  it('manually probes only explicit account ids', async () => {
    const wrapper = mountView(); await flushPromises()
    const input = wrapper.findAll('input').find(item => item.attributes('placeholder')?.includes('(1,2,3)'))
    expect(input).toBeDefined(); await input!.setValue('7, 8')
    await wrapper.get('[data-test="probe-accounts"]').trigger('click'); await flushPromises()
    expect(api.probeAccounts).toHaveBeenCalledWith([7, 8])
  })
})
