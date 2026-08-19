const EMAIL_FORMAT_PATTERN = /^[^\s@]+@[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}$/i
// 注册邮箱用于余额提醒，只允许当前明确支持投递的两个邮箱域名。
const SUPPORTED_REGISTRATION_EMAIL_DOMAINS = new Set(['qq.com', '163.com'])

export type RegistrationEmailErrorKey = 'auth.registrationEmailDomainNotAllowed'

export function normalizeRegistrationEmail(value: string): string {
  return value.trim().toLowerCase()
}

export function getRegistrationEmailErrorKey(value: string): RegistrationEmailErrorKey | null {
  const email = normalizeRegistrationEmail(value)
  const localPart = email.slice(0, email.lastIndexOf('@'))
  if (
    !EMAIL_FORMAT_PATTERN.test(email) ||
    localPart.startsWith('.') ||
    localPart.endsWith('.') ||
    email.includes('..')
  ) {
    return 'auth.registrationEmailDomainNotAllowed'
  }

  const domain = email.slice(email.lastIndexOf('@') + 1)
  if (!SUPPORTED_REGISTRATION_EMAIL_DOMAINS.has(domain)) {
    return 'auth.registrationEmailDomainNotAllowed'
  }
  return null
}
