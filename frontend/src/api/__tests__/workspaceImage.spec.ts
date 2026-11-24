import { afterEach, expect, it, vi } from 'vitest'
import { generateAIImage } from '../aiImage'

afterEach(() => vi.unstubAllGlobals())

it('参考图请求采用选定模型，旧页面仍默认 gpt-image-2', async () => {
  const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(Response.json({ data: [{ url: 'https://cdn.example/image.png' }] })))
  vi.stubGlobal('fetch', fetchMock)
  const options = { baseURL: 'https://relay.example', apiKey: 'test-only-key', prompt: '海报', sizeTier: '1K' as const,
    referenceImages: [new File(['image'], 'reference.png', { type: 'image/png' })] }
  await generateAIImage({ ...options, model: 'grok-imagine-image' })
  expect((fetchMock.mock.calls[0][1].body as FormData).get('model')).toBe('grok-imagine-image')
  await generateAIImage(options)
  expect((fetchMock.mock.calls[1][1].body as FormData).get('model')).toBe('gpt-image-2')
})

it.each([
  ['gpt-image-2.6', '1K', '1:1', '1024x1024'],
  ['gpt-image-2.6-flare', '1K', '16:9', '1024x640'],
  ['gpt-image-2.6-sunburst', '2K', '9:16', '1152x2048'],
  ['gpt-image-2.5-flare', '4K', '16:9', '3840x2160'],
  ['grok-imagine-image', '2K', '1:1', '2K']
] as const)('将 %s %s %s 映射到生成和编辑请求的 %s', async (model, sizeTier, aspectRatio, size) => {
  const fetchMock = vi.fn(async () => Response.json({ data: [{ url: 'https://cdn.example/image.png' }] }))
  vi.stubGlobal('fetch', fetchMock)
  const options = { baseURL: 'https://relay.example', apiKey: 'test-only-key', prompt: '海报', model, sizeTier, aspectRatio }
  await generateAIImage(options)
  await generateAIImage({ ...options, referenceImages: [new File(['image'], 'reference.png', { type: 'image/png' })] })
  const [[, generation], [, edit]] = vi.mocked(fetch).mock.calls
  const body = JSON.parse(String(generation?.body))
  expect(body.size).toBe(size)
  expect((edit?.body as FormData).get('size')).toBe(size)
  expect(body.response_format).toBe(model.startsWith('gpt-image-') ? undefined : 'b64_json')
})

it('用固定提示识别尺寸拒绝，不传播上游错误原文', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => Response.json({ error: { message: 'invalid image target size' } }, { status: 400 })))
  await expect(generateAIImage({ baseURL: 'https://relay.example', apiKey: 'test-only-key', prompt: '海报', sizeTier: '1K' }))
    .rejects.toMatchObject({ code: 'invalid-size' })
})
