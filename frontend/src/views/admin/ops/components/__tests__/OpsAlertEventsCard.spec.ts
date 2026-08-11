import { describe, it, expect, beforeEach, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import OpsAlertEventsCard from '../OpsAlertEventsCard.vue'

const mockListAlertEvents = vi.fn()
const mockGetAlertEvent = vi.fn()
const mockListAlertAccountDetails = vi.fn()

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    listAlertEvents: (...args: any[]) => mockListAlertEvents(...args),
    getAlertEvent: (...args: any[]) => mockGetAlertEvent(...args),
    listAlertAccountDetails: (...args: any[]) => mockListAlertAccountDetails(...args),
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
}))

vi.mock('@vueuse/core', () => ({
  useMediaQuery: () => ref(true),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      te: () => false,
    }),
  }
})

const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String },
  template: '<div v-if="show" class="dialog-stub"><slot /><slot name="footer" /></div>',
})

const event = {
  id: 91,
  rule_id: 18,
  severity: 'P1',
  status: 'firing',
  title: 'OpenAI 主账号异常',
  description: '503：upstream temporarily unavailable',
  dimensions: { diagnosis: 'http_503', platform: 'openai', group_id: 7, account_id: 9 },
  fired_at: '2026-08-11T10:00:00Z',
  email_sent: true,
  created_at: '2026-08-11T10:00:00Z',
}

const accountDetail = {
  id: 1,
  alert_event_id: 91,
  account_id: 9,
  account_name: 'OpenAI 主账号',
  platform: 'openai',
  group_id: 7,
  group_name: 'Plus 分组',
  diagnosis: 'http_503',
  error_phase: 'upstream',
  status_code: 503,
  occurred_at: '2026-08-11T10:00:00Z',
  error_log_id: 100,
  user_id: 11,
  user_email: 'user@example.com',
  api_key_id: 22,
  api_key_name: '生产 Key',
  request_id: 'req-internal',
  client_request_id: 'req-client',
  error_reason: '503：upstream temporarily unavailable',
  error_message: 'upstream temporarily unavailable',
  requested_model: 'gpt-5.5',
  upstream_model: 'gpt-5.5-2026-04-23',
  created_at: '2026-08-11T10:00:01Z',
}

describe('OpsAlertEventsCard account request details', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListAlertEvents.mockResolvedValueOnce([event]).mockResolvedValue([])
    mockGetAlertEvent.mockResolvedValue(event)
    mockListAlertAccountDetails.mockResolvedValue([accountDetail])
  })

  it('首屏显示账号与短原因，打开后展示用户、请求、分组、平台和模型信息', async () => {
    const wrapper = mount(OpsAlertEventsCard, {
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: { template: '<div />' },
          Icon: { template: '<span />' },
        },
      },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('OpenAI 主账号异常')
    expect(wrapper.text()).toContain('503：upstream temporarily unavailable')

    await wrapper.find('tbody tr').trigger('click')
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('user@example.com')
    expect(text).toContain('生产 Key')
    expect(text).toContain('req-internal')
    expect(text).toContain('req-client')
    expect(text).toContain('Plus 分组')
    expect(text).toContain('openai')
    expect(text).toContain('gpt-5.5')
    expect(text).toContain('gpt-5.5-2026-04-23')
    expect(text).toContain('upstream temporarily unavailable')

    wrapper.unmount()
  })
})
