import { describe, expect, it } from 'vitest'
import { extractApiErrorMessage } from '@/utils/apiError'

describe('extractApiErrorMessage', () => {
  it('保留后端返回的中文错误详情', () => {
    expect(extractApiErrorMessage({ message: '账号已被禁用' }, '操作失败')).toBe('账号已被禁用')
  })

  it('无法识别纯英文错误时使用中文兜底', () => {
    expect(extractApiErrorMessage({ message: 'Unexpected provider failure' }, '操作失败')).toBe(
      '操作失败'
    )
  })

  it('未提供兜底时也不会返回英文默认提示', () => {
    expect(extractApiErrorMessage({})).toBe('发生未知错误。')
  })
})
