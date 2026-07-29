import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminUser } from '@/types'
import UserCreateModal from '../UserCreateModal.vue'
import UserEditModal from '../UserEditModal.vue'

const { createUser, updateUser } = vi.hoisted(() => ({
  createUser: vi.fn(),
  updateUser: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      create: createUser,
      update: updateUser
    },
    userAttributes: {
      updateUserAttributeValues: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: (operation: () => Promise<unknown>) => operation() }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => ''
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
}

const mountOptions = {
  global: {
    stubs: {
      BaseDialog: BaseDialogStub,
      Icon: true,
      TotpStepUpDialog: true,
      UserAttributeForm: true
    }
  }
}

const createAdminUser = (adminUsageMultiplier: number | null): AdminUser => ({
  id: 42,
  email: 'admin-multiplier@example.com',
  username: 'admin-multiplier',
  role: 'user',
  balance: 0,
  concurrency: 1,
  rpm_limit: 0,
  status: 'active',
  allowed_groups: [],
  balance_notify_enabled: false,
  balance_notify_threshold: null,
  balance_notify_extra_emails: [],
  created_at: '2026-07-27T00:00:00Z',
  updated_at: '2026-07-27T00:00:00Z',
  notes: '',
  admin_usage_multiplier: adminUsageMultiplier
})

describe('admin user usage multiplier fields', () => {
  beforeEach(() => {
    createUser.mockReset().mockResolvedValue({})
    updateUser.mockReset().mockResolvedValue({})
  })

  it('keeps an explicit zero when creating a user', async () => {
    const wrapper = mount(UserCreateModal, {
      ...mountOptions,
      props: { show: true }
    })

    await wrapper.get('input[type="email"]').setValue('zero@example.com')
    await wrapper.get('input[required][type="text"]').setValue('secret123')
    await wrapper.get('[data-test="admin-usage-multiplier"]').setValue('0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(createUser).toHaveBeenCalledWith(expect.objectContaining({
      admin_usage_multiplier: 0
    }))
  })

  it('accepts and submits a four-decimal multiplier when creating a user', async () => {
    const wrapper = mount(UserCreateModal, {
      ...mountOptions,
      props: { show: true }
    })

    await wrapper.get('input[type="email"]').setValue('decimal@example.com')
    await wrapper.get('input[required][type="text"]').setValue('secret123')
    const input = wrapper.get('[data-test="admin-usage-multiplier"]')
    expect(input.attributes('step')).toBe('0.0001')
    await input.setValue('1.1111')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(createUser).toHaveBeenCalledWith(expect.objectContaining({
      admin_usage_multiplier: 1.1111
    }))
  })

  it('clears an existing user multiplier to restore group inheritance', async () => {
    const wrapper = mount(UserEditModal, {
      ...mountOptions,
      props: { show: true, user: createAdminUser(1.5) }
    })

    const input = wrapper.get('[data-test="admin-usage-multiplier"]')
    expect((input.element as HTMLInputElement).value).toBe('1.5')
    await input.setValue('')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updateUser).toHaveBeenCalledWith(42, expect.objectContaining({
      clear_admin_usage_multiplier: true
    }))
    expect(updateUser.mock.calls[0][1]).not.toHaveProperty('admin_usage_multiplier')
  })
})
