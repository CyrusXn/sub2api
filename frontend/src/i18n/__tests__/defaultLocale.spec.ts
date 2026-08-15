import { beforeEach, describe, expect, it, vi } from 'vitest'
import zhCommon from '@/i18n/locales/zh/common'

describe('默认语言', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it('未保存语言偏好时默认使用中文', async () => {
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('en-US')

    const { getLocale } = await import('@/i18n')

    expect(getLocale()).toBe('zh')
  })

  it('账号或密码错误显示明确的中文提示', () => {
    expect(zhCommon.auth.errors.INVALID_CREDENTIALS).toBe('账号或密码错误')
  })
})
