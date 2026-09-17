import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchKeyTestModels, streamKeyTest } from '../keyTest'

const encoder = new TextEncoder()
function sseResponse(chunks: Uint8Array[]) {
  return new Response(new ReadableStream({
    start(controller) {
      chunks.forEach(chunk => controller.enqueue(chunk))
      controller.close()
    }
  }), { headers: { 'Content-Type': 'text/event-stream; charset=utf-8' } })
}
const options = () => ({
  baseUrl: 'https://relay.example/prefix/v1/', apiKey: 'test-only-key', model: 'text-model',
  messages: [{ role: 'user' as const, content: '你好' }],
  signal: new AbortController().signal, onText: vi.fn()
})
afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs() })

describe('密钥测试网关请求', () => {
  it.each([
    [true, 'https://relay.example', '/v1/models'],
    [true, 'https://relay.example/v1/', '/v1/models'],
    [true, 'https://other.example', 'https://other.example/v1/models'],
    [false, 'https://relay.example', 'https://relay.example/v1/models']
  ])('开发代理只接收同一中转目标（DEV=%s，地址=%s）', async (dev, baseUrl, expected) => {
    vi.stubEnv('DEV', dev)
    vi.stubEnv('VITE_DEV_PROXY_TARGET', 'https://relay.example')
    const fetchMock = vi.fn().mockResolvedValue(Response.json({ data: [] }))
    vi.stubGlobal('fetch', fetchMock)
    await fetchKeyTestModels(baseUrl, 'test-only-key', new AbortController().signal)
    expect(fetchMock).toHaveBeenCalledWith(expected, expect.any(Object))
  })

  it('使用选中密钥读取模型并保留地址前缀，不携带管理会话', async () => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json({ data: [{ id: 'model-a' }, { id: 'model-a' }, { id: 'model-b' }, { id: null }] }))
    vi.stubGlobal('fetch', fetchMock)
    const opts = options()
    expect(await fetchKeyTestModels(opts.baseUrl, opts.apiKey, opts.signal)).toEqual(['model-a', 'model-b'])
    expect(fetchMock).toHaveBeenCalledWith('https://relay.example/prefix/v1/models', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer test-only-key' }), credentials: 'omit', redirect: 'error'
    }))
  })

  it('处理跨 UTF-8 字节分包、CRLF、多行 data 和心跳，只回调实际文字', async () => {
    const bytes = encoder.encode(': ping\r\n\r\ndata: {"choices":[{"delta":{"role":"assistant"}}]}\r\n\r\ndata: {"choices":\r\ndata: [{"delta":{"content":"你好"}}]}\r\n\r\ndata: [DONE]\r\n\r\n')
    const fetchMock = vi.fn().mockResolvedValue(sseResponse(Array.from(bytes, byte => new Uint8Array([byte]))))
    vi.stubGlobal('fetch', fetchMock)
    const opts = options()
    await streamKeyTest(opts)
    expect(opts.onText.mock.calls).toEqual([['你好']])
    expect(fetchMock).toHaveBeenCalledWith('https://relay.example/prefix/v1/chat/completions', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ model: 'text-model', messages: opts.messages, stream: true, max_tokens: 2048 })
    }))
  })

  it('支持最后一个事件没有换行的正常结束', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse([encoder.encode('data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}')])))
    const opts = options()
    await streamKeyTest(opts)
    expect(opts.onText).toHaveBeenCalledWith('OK')
  })

  it('收到 finish_reason 后继续读取尾部帧，不忽略后续错误', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse([
      encoder.encode('data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}\n\n'),
      encoder.encode('data: {"error":{"message":"stream failed"}}\n\n')
    ])))
    await expect(streamKeyTest(options())).rejects.toMatchObject({ code: 'streamError' })
  })

  it.each([
    ['data: [DONE]\n\n', 'empty'],
    ['data: {"choices":[{"delta":{"content":"半截"}}]}\n\n', 'incomplete'],
    ['data: {"error":{"message":"private upstream detail"}}\n\n', 'streamError'],
    ['data: not-json\n\n', 'invalidResponse']
  ])('异常响应不会误报成功：%s', async (body, code) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse([encoder.encode(body)])))
    await expect(streamKeyTest(options())).rejects.toMatchObject({ code, message: code })
  })

  it('HTTP 错误不回显上游敏感正文', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('private upstream detail', { status: 403 })))
    await expect(streamKeyTest(options())).rejects.toMatchObject({ code: 'http', status: 403, message: 'http' })
  })

  it('拒绝伪装成功的 HTML 页面', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>login</html>', { headers: { 'Content-Type': 'text/html' } })))
    await expect(streamKeyTest(options())).rejects.toMatchObject({ code: 'invalidResponse' })
  })

  it('结束前取消请求不算成功，并释放流', async () => {
    const controller = new AbortController()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sseResponse([encoder.encode('data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}\n\n')])))
    const opts = { ...options(), signal: controller.signal, onText: () => controller.abort() }
    await expect(streamKeyTest(opts)).rejects.toMatchObject({ name: 'AbortError' })
  })
})
