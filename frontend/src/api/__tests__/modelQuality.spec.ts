import { describe, expect, it, vi } from 'vitest'
import { extractModelQualityHTML, modelQualityAPI, modelQualityPrompt, safeModelQualityHTML } from '../modelQuality'
import { apiClient } from '../client'
vi.mock('../client', () => ({ apiClient: { get: vi.fn() } }))
describe('智检用户搜索协议', () => {
 it('旧后端忽略用户筛选时提示升级，不能将全部记录当作搜索结果', async () => {
  const params = { page: 1, theme: 'candy' as const, status: '', group: '', model: '', user: 'ad***' }
  vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [], total: 0 } })
  await expect(modelQualityAPI.list(params)).rejects.toMatchObject({ status: 501 })
  vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: [], total: 0, user_filter: 'ad***' } })
  await expect(modelQualityAPI.list(params)).resolves.toMatchObject({ items: [], total: 0 })
 })
})
describe('智检默认提示词', () => {
 it('官 key 默认探测内容为 hi', () => {
  expect(modelQualityPrompt('official', '')).toBe('hi')
 })
 it('鹈鹕测试使用用户指定的简短提示词', () => {
  expect(modelQualityPrompt('pelican', '')).toBe('画一个鹈鹕骑自行车的svg 用h5展示')
 })
})
describe('智检 HTML 隔离', () => {
 it.each([
  ['前言\n```html\n<HTML><body><svg></svg></body></HTML>\n```\n后记', '<HTML><body><svg></svg></body></HTML>'],
  ['前言\n```svg\n<svg><text>作品</text></svg>\n```\n后记', '<svg><text>作品</text></svg>'],
  ['```html\n<style>svg{color:red}</style><div><svg></svg></div>\n```', '<style>svg{color:red}</style><div><svg></svg></div>'],
  ['前言\n```html\n<html><body><svg><circle', '<html><body><svg><circle'],
  ['只有文字', '']
 ])('只提取 HTML/SVG 并兼容流式未闭合内容：%s', (input, expected) => {
  expect(extractModelQualityHTML(input)).toBe(expected)
 })
 it('保留 SVG 内部引用，但移除外部资源', () => {
  const safe = safeModelQualityHTML('<svg><defs><circle id="wheel" /></defs><use href="#wheel"/><use href="https://evil.example/a.svg#wheel"/></svg>')
  expect(safe).toContain('href="#wheel"')
  expect(safe).not.toContain('https://evil.example')
 })
 it('HTML 代码围栏和外围说明不会泄漏到预览页面', () => {
  const safe = safeModelQualityHTML('这是生成的页面：\n```html\n<!doctype html><html><body><svg><text>pelican</text></svg></body></html>\n```\n以上为 HTML。')
  expect(safe).not.toContain('```html')
  expect(safe).not.toContain('这是生成的页面')
  expect(safe).not.toContain('以上为 HTML')
  expect(safe).toContain('<text>pelican</text>')
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
