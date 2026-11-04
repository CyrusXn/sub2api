import { describe, expect, it } from 'vitest'
import { normalizeUserFacingErrorMessage } from '../userFacingMessage'

describe('normalizeUserFacingErrorMessage', () => {
  it('将账号测试的上游无可用账号错误转换为明确中文提示', () => {
    const message = 'API returned 503: {"error":{"message":"No available accounts: no available accounts","type":"api_error"},"type":"error"}'
    expect(normalizeUserFacingErrorMessage(message, '发生未知错误')).toBe(
      '上游分组暂无可用账号（HTTP 503），请检查上游账号池或联系上游服务商。'
    )
  })

  it('其他上游 HTTP 错误只展示状态码，不暴露响应正文', () => {
    expect(normalizeUserFacingErrorMessage('API returned 401: {"error":{"message":"invalid key: test-secret"}}')).toBe(
      '上游请求失败（HTTP 401），请检查上游配置或稍后重试。'
    )
    expect(normalizeUserFacingErrorMessage('API returned 502: <html>Bad Gateway</html>')).toBe(
      '上游请求失败（HTTP 502），请检查上游配置或稍后重试。'
    )
  })

  it.each([
    'API returned 401: {"error":{"message":"密钥无效: test-secret"}}',
    'API returned 401: <html>认证失败 test-secret</html>',
  ])('中文上游响应也必须隐藏原始正文：%s', message => {
    expect(normalizeUserFacingErrorMessage(message)).toBe(
      '上游请求失败（HTTP 401），请检查上游配置或稍后重试。'
    )
  })

  it('保留已有中文、已知英文翻译和未知错误兜底', () => {
    expect(normalizeUserFacingErrorMessage('余额不足')).toBe('余额不足')
    expect(normalizeUserFacingErrorMessage('Unknown error')).toBe('发生未知错误。')
    expect(normalizeUserFacingErrorMessage('unrecognized', '测试失败')).toBe('测试失败')
    expect(normalizeUserFacingErrorMessage(null, '测试失败')).toBe('测试失败')
  })
})
