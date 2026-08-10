import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import VersionBadge from '@/components/common/VersionBadge.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: {
    currentVersion: '0.1.172',
    latestVersion: '0.1.172',
    hasUpdate: false,
    releaseInfo: null,
    buildType: 'custom',
    edition: 'xnkaixin.20260808',
    versionLoading: false,
    fetchVersion: vi.fn().mockResolvedValue(undefined),
  },
  authStore: {
    isAdmin: true,
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

vi.mock('@/api/admin/system', () => ({
  performUpdate: vi.fn(),
  restartService: vi.fn(),
  getRollbackVersions: vi.fn(),
  rollback: vi.fn(),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('VersionBadge user-visible version', () => {
  it('shows only the semantic version even when a custom edition is present', () => {
    const wrapper = mount(VersionBadge, {
      global: {
        stubs: { Icon: true },
      },
    })

    // edition 仅用于镜像追踪，任何用户可见版本文案都不能拼接定制标识。
    expect(wrapper.get('button').text()).toContain('v0.1.172')
    expect(wrapper.text()).not.toContain('xnkaixin')
    wrapper.unmount()
  })

  it('shows only the semantic version to non-admin users', () => {
    authStore.isAdmin = false
    const wrapper = mount(VersionBadge, {
      props: {
        version: '0.1.173-xnkaixin.20260810',
      },
      global: {
        stubs: { Icon: true },
      },
    })

    // 普通用户同样只能看到纯语义版本，不能暴露内部构建标识。
    expect(wrapper.text()).toContain('v0.1.173')
    expect(wrapper.text()).not.toContain('xnkaixin')
    wrapper.unmount()
    authStore.isAdmin = true
  })
})
