import { describe, expect, it } from 'vitest'
import { buildAuthErrorMessage } from '@/utils/authError'

describe('buildAuthErrorMessage', () => {
  it('prefers response detail message when available', () => {
    const message = buildAuthErrorMessage(
      {
        response: {
          data: {
            detail: '详细错误',
            message: '普通错误'
          }
        },
      },
      { fallback: '默认错误' }
    )
    expect(message).toBe('详细错误')
  })

  it('falls back to response message when detail is unavailable', () => {
    const message = buildAuthErrorMessage(
      {
        response: {
          data: {
            message: '普通错误'
          }
        },
      },
      { fallback: '默认错误' }
    )
    expect(message).toBe('普通错误')
  })

  it('falls back to error.message when response payload is unavailable', () => {
    const message = buildAuthErrorMessage(
      {
        message: '请求失败'
      },
      { fallback: '默认错误' }
    )
    expect(message).toBe('请求失败')
  })

  it('uses fallback when no message can be extracted', () => {
    expect(buildAuthErrorMessage({}, { fallback: '默认错误' })).toBe('默认错误')
  })

  it('将旧后端的邮箱验证英文错误转换为中文提示', () => {
    const message = buildAuthErrorMessage(
      {
        message: 'email verification is required'
      },
      { fallback: '注册失败，请重试。' }
    )

    expect(message).toBe('当前服务仍要求邮箱验证，请等待后端更新后重试。')
  })

  it('不向用户直接展示无法识别的纯英文认证错误', () => {
    const message = buildAuthErrorMessage(
      {
        response: {
          data: {
            detail: 'Unexpected authentication provider failure'
          }
        }
      },
      { fallback: '登录失败，请重试。' }
    )

    expect(message).toBe('登录失败，请重试。')
  })
})
