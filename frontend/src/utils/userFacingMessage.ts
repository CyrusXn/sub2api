const KNOWN_ENGLISH_ERROR_MESSAGES: Record<string, string> = {
  'email verification is required': '当前服务仍要求邮箱验证，请等待后端更新后重试。',
  'network error. please check your connection.': '网络错误，请检查网络连接。',
  'unknown error': '发生未知错误。',
}

const CHINESE_TEXT_PATTERN = /[\u3400-\u9fff]/

/**
 * 面向中国客户的错误提示必须为中文；已知英文错误给出明确翻译，未知英文使用业务兜底文案。
 */
export function normalizeUserFacingErrorMessage(
  message: unknown,
  fallback = '操作失败，请稍后重试。',
): string {
  const chineseFallback = CHINESE_TEXT_PATTERN.test(fallback) ? fallback : '操作失败，请稍后重试。'
  if (typeof message !== 'string') return chineseFallback

  const normalized = message.trim()
  if (!normalized) return chineseFallback
  // 账号测试把上游状态包在 SSE 错误中；保留可诊断原因，避免直接展示上游正文。
  const upstreamError = /^API returned ([45]\d{2}):\s*([\s\S]*)$/.exec(normalized)
  if (upstreamError) {
    const [, status, body] = upstreamError
    try {
      const payload = JSON.parse(body ?? '')
      const reason = payload?.error?.message
      if (typeof reason === 'string' && /^no available accounts(?:: no available accounts)?$/i.test(reason.trim())) {
        return `上游分组暂无可用账号（HTTP ${status}），请检查上游账号池或联系上游服务商。`
      }
    } catch {
      // 非 JSON 响应仍保留状态码，不将 HTML 或敏感正文传给页面。
    }
    return `上游请求失败（HTTP ${status}），请检查上游配置或稍后重试。`
  }
  if (CHINESE_TEXT_PATTERN.test(normalized)) return normalized
  return KNOWN_ENGLISH_ERROR_MESSAGES[normalized.toLowerCase()] ?? chineseFallback
}
