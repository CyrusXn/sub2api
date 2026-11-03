import { describe, expect, it } from 'vitest'
import { safeModelQualityHTML } from '../modelQuality'
describe('智检 HTML 隔离', () => {
 it('保留 SVG 内部引用，但移除外部资源', () => {
  const safe = safeModelQualityHTML('<svg><defs><circle id="wheel" /></defs><use href="#wheel"/><use href="https://evil.example/a.svg#wheel"/></svg>')
  expect(safe).toContain('href="#wheel"')
  expect(safe).not.toContain('https://evil.example')
 })
 it('去除脚本、事件、嵌入、外部链接和表单，保留 SVG / CSS 动画', () => {
  const safe = safeModelQualityHTML('<html><body><script>alert(1)</script><svg onload="alert(2)"><circle cx="20" /></svg><iframe src="https://evil.example"></iframe><a href="https://evil.example">跳转</a><form></form><style>circle{animation:move 1s}</style></body></html>')
  expect(safe).not.toContain('<script')
  expect(safe).not.toContain('onload')
  expect(safe).not.toContain('<iframe')
  expect(safe).not.toContain('<form')
  expect(safe).not.toContain('href=')
  expect(safe).toContain('<circle')
  expect(safe).toContain('Content-Security-Policy')
  expect(safe).toContain("default-src 'none'")
 })
})
