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
