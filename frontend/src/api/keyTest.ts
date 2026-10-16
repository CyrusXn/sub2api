export interface TestMessage {
  role: 'user' | 'assistant'
  content: string
}

export class KeyTestError extends Error {
  constructor(public code: 'http' | 'invalidResponse' | 'streamError' | 'incomplete' | 'empty', public status?: number) {
    super(code)
  }
}

function endpoint(baseUrl: string, path: string, version: '/v1' | '/v1beta' = '/v1'): string {
  const base = (baseUrl.trim() || window.location.origin).replace(/\/+$/, '').replace(/\/v1(?:beta)?$/i, '')
  const devTarget = import.meta.env.VITE_DEV_PROXY_TARGET?.trim().replace(/\/+$/, '')
  // 仅本地且目标完全一致时复用 Vite 代理，避免真实中转站拒绝 localhost 跨域。
  if (import.meta.env.DEV && devTarget && base === devTarget) {
    return `${version}/${path}`
  }
  return `${base}${version}/${path}`
}

// 使用行内密钥访问网关，不能经过携带管理登录令牌的 apiClient。
function request(baseUrl: string, apiKey: string, path: string, signal: AbortSignal, body?: unknown, version: '/v1' | '/v1beta' = '/v1') {
  return fetch(endpoint(baseUrl, path, version), {
    method: body ? 'POST' : 'GET',
    headers: {
      ...(version === '/v1beta' ? { 'x-goog-api-key': apiKey } : { Authorization: `Bearer ${apiKey}` }),
      Accept: body && version === '/v1' ? 'text/event-stream' : 'application/json',
      ...(body ? { 'Content-Type': 'application/json' } : {})
    },
    body: body ? JSON.stringify(body) : undefined,
    credentials: 'omit',
    cache: 'no-store',
    redirect: 'error',
    signal
  })
}

export async function fetchKeyTestModels(baseUrl: string, apiKey: string, signal: AbortSignal): Promise<string[]> {
  const response = await request(baseUrl, apiKey, 'models', signal)
  if (!response.ok) throw new KeyTestError('http', response.status)
  const payload = await response.json()
  if (!Array.isArray(payload?.data)) throw new KeyTestError('invalidResponse')
  return [...new Set<string>(payload.data
    .map((model: { id?: unknown }) => model?.id)
    .filter((id: unknown): id is string => typeof id === 'string' && id.trim().length > 0))]
}

export async function streamKeyTest(options: {
  baseUrl: string
  apiKey: string
  model: string
  protocol?: 'openai' | 'gemini'
  messages: TestMessage[]
  signal: AbortSignal
  onText: (text: string) => void
}): Promise<void> {
  // Gemini 按模型原生端点测试；保留其他调用者的 OpenAI 流式契约。
  if (options.protocol === 'gemini') {
    const response = await request(options.baseUrl, options.apiKey, `models/${encodeURIComponent(options.model)}:generateContent`, options.signal, {
      contents: options.messages.map(message => ({
        role: message.role === 'assistant' ? 'model' : 'user', parts: [{ text: message.content }]
      })),
      generationConfig: { maxOutputTokens: 2048 }
    }, '/v1beta')
    options.signal.throwIfAborted()
    if (!response.ok) throw new KeyTestError('http', response.status)
    if (!response.headers.get('content-type')?.includes('application/json')) throw new KeyTestError('invalidResponse')
    let payload
    try {
      payload = await response.json()
    } catch {
      options.signal.throwIfAborted()
      throw new KeyTestError('invalidResponse')
    }
    options.signal.throwIfAborted()
    if (payload?.error || payload?.promptFeedback?.blockReason) throw new KeyTestError('streamError')
    if (!Array.isArray(payload?.candidates)) throw new KeyTestError('invalidResponse')
    if (!payload.candidates.length) throw new KeyTestError('empty')
    const candidate = payload.candidates[0]
    if (!candidate || typeof candidate !== 'object') throw new KeyTestError('invalidResponse')
    if (candidate.finishReason && !['STOP', 'MAX_TOKENS'].includes(candidate.finishReason)) throw new KeyTestError('streamError')
    const parts = candidate.content?.parts
    if (!Array.isArray(parts)) throw new KeyTestError('invalidResponse')
    const text = parts.filter(part => part && !part.thought && typeof part.text === 'string').map(part => part.text).join('')
    if (!text.trim()) throw new KeyTestError('empty')
    options.onText(text)
    options.signal.throwIfAborted()
    return
  }
  const response = await request(options.baseUrl, options.apiKey, 'chat/completions', options.signal, {
    model: options.model,
    messages: options.messages,
    stream: true,
    max_tokens: 2048
  })
  if (!response.ok) throw new KeyTestError('http', response.status)
  if (!response.headers.get('content-type')?.includes('text/event-stream') || !response.body) {
    throw new KeyTestError('invalidResponse')
  }

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let completed = false
  let finished = false
  let hasText = false
  const processEvent = (event: string) => {
    const data = event.split(/\r?\n/)
      .filter(line => line.startsWith('data:'))
      .map(line => line.slice(5).replace(/^ /, '')).join('\n')
    if (!data) return
    if (data.trim() === '[DONE]') {
      completed = true
      return
    }
    let payload
    try {
      payload = JSON.parse(data)
    } catch {
      throw new KeyTestError('invalidResponse')
    }
    // 不回显上游原始错误，避免泄露请求头、密钥或上游敏感详情。
    if (payload?.error || payload?.type === 'error') throw new KeyTestError('streamError')
    const choice = payload?.choices?.[0]
    const content = choice?.delta?.content
    if (typeof content === 'string' && content.length > 0) {
      hasText = true
      options.onText(content)
    }
    // finish_reason 后可能还有用量帧，继续读到 DONE 或正常 EOF，避免提前取消上游。
    if (choice?.finish_reason != null) finished = true
  }

  try {
    while (!completed) {
      const { value, done } = await reader.read()
      buffer += decoder.decode(value, { stream: !done })
      let boundary
      while ((boundary = /\r?\n\r?\n/.exec(buffer)) !== null) {
        processEvent(buffer.slice(0, boundary.index))
        buffer = buffer.slice(boundary.index + boundary[0].length)
        if (completed) break
      }
      if (done) {
        if (!completed && buffer.trim()) processEvent(buffer)
        break
      }
    }
    options.signal.throwIfAborted()
    if (!completed && !finished) throw new KeyTestError('incomplete')
    if (!hasText) throw new KeyTestError('empty')
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
