import { describe, expect, it } from 'vitest'
import { getRegistrationEmailErrorKey } from '@/utils/registrationEmail'

describe('registrationEmail', () => {
  it('accepts only qq.com addresses', () => {
    expect(getRegistrationEmailErrorKey(' User@QQ.COM ')).toBeNull()
    expect(getRegistrationEmailErrorKey('user@163.com')).toBe(
      'auth.registrationEmailDomainNotAllowed'
    )
    expect(getRegistrationEmailErrorKey('user@gmail.com')).toBe(
      'auth.registrationEmailDomainNotAllowed'
    )
  })
})
