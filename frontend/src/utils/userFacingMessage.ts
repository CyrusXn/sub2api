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
  if (CHINESE_TEXT_PATTERN.test(normalized)) return normalized

  return KNOWN_ENGLISH_ERROR_MESSAGES[normalized.toLowerCase()] ?? chineseFallback
}
