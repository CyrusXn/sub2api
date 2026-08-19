import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RegisterView from '@/views/auth/RegisterView.vue'

const { getPublicSettingsMock, registerMock, showErrorMock } = vi.hoisted(() => ({
  getPublicSettingsMock: vi.fn(),
  registerMock: vi.fn(),
  showErrorMock: vi.fn()
}))

const publicSettings = {
  registration_enabled: true,
  email_verify_enabled: false,
  promo_code_enabled: false,
  invitation_code_enabled: false,
  affiliate_enabled: true,
  turnstile_enabled: true,
  turnstile_site_key: 'site-key',
  site_name: 'Sub2API',
  registration_email_suffix_whitelist: [],
  linuxdo_oauth_enabled: false,
  wechat_oauth_enabled: false,
  oidc_oauth_enabled: false,
  github_oauth_enabled: false,
  google_oauth_enabled: false
}

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ query: {} })
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key
    }
  }),
  useI18n: () => ({
    t: (key: string) => {
      const messages: Record<string, string> = {
        'auth.invalidEmail': '请输入正确的邮箱地址',
        'auth.registrationEmailDomainNotAllowed': '仅支持 qq.com 和 163.com 邮箱注册',
        'auth.emailDomainRegistrationLimit':
          '该邮箱域名无法注册新账户。请使用主流邮箱注册；如需使用企业邮箱，请联系客服添加域名白名单。'
      }
      return messages[key] ?? key
    },
    locale: { value: 'en' }
  })
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ register: (...args: unknown[]) => registerMock(...args) }),
  useAppStore: () => ({
    showError: (...args: unknown[]) => showErrorMock(...args),
    showSuccess: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    getPublicSettings: (...args: unknown[]) => getPublicSettingsMock(...args)
  }
})

function mountRegister() {
  return mount(RegisterView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true,
        TurnstileWidget: { template: '<div data-testid="turnstile-widget" />' },
        LoginAgreementPrompt: true,
        EmailOAuthButtons: true,
        LinuxDoOAuthSection: true,
        WechatOAuthSection: true,
        OidcOAuthSection: true,
        RouterLink: true,
        transition: false
      }
    }
  })
}

describe('RegisterView invitation layout', () => {
  beforeEach(() => {
    getPublicSettingsMock.mockReset()
    registerMock.mockReset()
    showErrorMock.mockReset()
    getPublicSettingsMock.mockResolvedValue(publicSettings)
    registerMock.mockResolvedValue({})
  })

  it('keeps the optional affiliate invitation field before Turnstile', async () => {
    const wrapper = mountRegister()
    await flushPromises()

    const invitationField = wrapper.get('[data-testid="affiliate-invitation-field"]')
    const turnstile = wrapper.get('[data-testid="registration-turnstile"]')

    expect(invitationField.get('input').attributes('id')).toBe('affiliate_code')
    expect(invitationField.text()).toContain('common.optional')
    expect(
      invitationField.element.compareDocumentPosition(turnstile.element) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  })

  it('uses the mandatory invitation field without duplicating the affiliate field', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      invitation_code_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()

    expect(wrapper.find('[data-testid="affiliate-invitation-field"]').exists()).toBe(false)
    expect(wrapper.get('#invitation_code').exists()).toBe(true)
  })

  it('renders the promo code field when the public switch is enabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      promo_code_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()

    expect(wrapper.get('#promo_code').exists()).toBe(true)
  })

  it('accepts a supported email and registers it directly even when email verification is enabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      email_verify_enabled: true,
      registration_email_suffix_whitelist: ['allowed.com'],
      registration_email_domain_quota_enabled: false
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('  USER@QQ.COM  ')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'user@qq.com' })
    )
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  it('shows one supported-domain message for a malformed email', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({ ...publicSettings, turnstile_enabled: false })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('随便填写')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')

    expect(registerMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledOnce()
    expect(showErrorMock).toHaveBeenCalledWith('仅支持 qq.com 和 163.com 邮箱注册')
  })

  it('shows the supported-domain message for another valid email domain', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({ ...publicSettings, turnstile_enabled: false })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('user@gmail.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')

    expect(registerMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledOnce()
    expect(showErrorMock).toHaveBeenCalledWith('仅支持 qq.com 和 163.com 邮箱注册')
  })

  it('shows the localized duplicate account message returned by the backend', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({ ...publicSettings, turnstile_enabled: false })
    registerMock.mockRejectedValueOnce({
      reason: 'EMAIL_EXISTS',
      message: 'raw backend message'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('existing@qq.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('auth.errors.EMAIL_EXISTS')
  })

  it('renders account fields and the WeChat group contact on the registration page', async () => {
    const wrapper = mountRegister()
    await flushPromises()

    expect(wrapper.get('#email').attributes('type')).toBe('email')
    expect(wrapper.text()).toContain('auth.emailLabel')

    const contactNotice = wrapper.get('[data-testid="wechat-contact-notice"]')
    expect(contactNotice.classes()).toEqual(
      expect.arrayContaining(['text-base', 'font-semibold', 'border-red-200', 'bg-red-50'])
    )

    const contactID = wrapper.get('[data-testid="wechat-contact-id"]')
    expect(contactID.text()).toBe('auth.wechatGroupContactId')
    expect(contactID.classes()).toEqual(
      expect.arrayContaining(['text-xl', 'font-extrabold', 'text-red-600'])
    )
  })
})
