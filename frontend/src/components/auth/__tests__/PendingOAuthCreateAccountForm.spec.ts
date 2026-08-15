import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import PendingOAuthCreateAccountForm from '../PendingOAuthCreateAccountForm.vue'

const getPublicSettings = vi.fn()
const showError = vi.fn()
const turnstileReset = vi.fn()
const verifyAction = vi.fn()

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    getPublicSettings: (...args: any[]) => getPublicSettings(...args)
  }
})

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError })
}))

describe('PendingOAuthCreateAccountForm', () => {
  beforeEach(() => {
    getPublicSettings.mockReset()
    showError.mockReset()
    turnstileReset.mockReset()
    verifyAction.mockReset()
    getPublicSettings.mockResolvedValue({
      turnstile_enabled: false,
      turnstile_site_key: ''
    })
  })

  it('提交任意账号和密码，不再要求邮箱验证码', async () => {
    const wrapper = mount(PendingOAuthCreateAccountForm, {
      props: {
        testIdPrefix: 'oidc',
        initialEmail: '',
        isSubmitting: false
      }
    })

    await wrapper.get('[data-testid="oidc-create-account-email"]').setValue('  任意账号  ')
    await wrapper.get('[data-testid="oidc-create-account-password"]').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')

    expect(wrapper.find('[data-testid="oidc-create-account-verify-code"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="oidc-create-account-send-code"]').exists()).toBe(false)
    expect(wrapper.emitted('submit')).toEqual([
      [{ email: '任意账号', password: 'secret-123' }]
    ])
  })

  it('启用邀请码时随创建请求提交邀请码', async () => {
    getPublicSettings.mockResolvedValue({
      invitation_code_enabled: true,
      turnstile_enabled: false,
      turnstile_site_key: ''
    })
    const wrapper = mount(PendingOAuthCreateAccountForm, {
      props: {
        testIdPrefix: 'linuxdo',
        initialEmail: 'prefill-account',
        isSubmitting: false
      }
    })

    await flushPromises()
    await wrapper.get('[data-testid="linuxdo-create-account-password"]').setValue('secret-123')
    await wrapper.get('[data-testid="linuxdo-create-account-invitation-code"]').setValue(' INVITE123 ')
    await wrapper.get('form').trigger('submit.prevent')

    expect(wrapper.emitted('submit')).toEqual([
      [{
        email: 'prefill-account',
        password: 'secret-123',
        invitationCode: 'INVITE123'
      }]
    ])
  })

  it('启用 Turnstile 时必须先完成人机验证', async () => {
    getPublicSettings.mockResolvedValue({
      turnstile_enabled: true,
      turnstile_site_key: 'site-key'
    })
    const wrapper = mount(PendingOAuthCreateAccountForm, {
      props: {
        testIdPrefix: 'oidc',
        initialEmail: 'account-name',
        isSubmitting: false
      },
      global: {
        stubs: {
          TurnstileWidget: {
            template: '<button data-testid="turnstile-verify" @click="$emit(\'verify\', \'turnstile-token\')">verify</button>',
            methods: { reset: turnstileReset }
          }
        }
      }
    })

    await flushPromises()
    await wrapper.get('[data-testid="oidc-create-account-password"]').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    expect(wrapper.emitted('submit')).toBeUndefined()

    await wrapper.get('[data-testid="turnstile-verify"]').trigger('click')
    await wrapper.get('[data-testid="oidc-create-account-submit"]').trigger('click')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      email: 'account-name',
      password: 'secret-123',
      turnstileToken: 'turnstile-token'
    })
  })

  it('动作式验证码只在创建账号时获取一次凭据', async () => {
    getPublicSettings.mockResolvedValue({
      turnstile_enabled: false,
      turnstile_site_key: '',
      tencent_captcha_enabled: true,
      tencent_captcha_app_id: 'tencent-app-id'
    })
    verifyAction.mockResolvedValue({ token: 'ticket-1', randstr: '@rand-1' })
    const CaptchaChallengeStub = defineComponent({
      setup(_, { expose }) {
        expose({ verifyAction, reset: turnstileReset })
        return () => h('div')
      }
    })
    const wrapper = mount(PendingOAuthCreateAccountForm, {
      props: {
        testIdPrefix: 'oidc',
        initialEmail: 'account-name',
        isSubmitting: false
      },
      global: { stubs: { TurnstileWidget: CaptchaChallengeStub } }
    })

    await flushPromises()
    await wrapper.get('[data-testid="oidc-create-account-password"]').setValue('secret-123')
    await wrapper.get('[data-testid="oidc-create-account-submit"]').trigger('click')
    await flushPromises()

    expect(verifyAction).toHaveBeenCalledOnce()
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      tencentCaptchaTicket: 'ticket-1',
      tencentCaptchaRandstr: '@rand-1'
    })
  })
})
