export const AI_IMAGE_MODEL = 'gpt-image-2'
export const AI_IMAGE_TIMEOUT_MS = 5 * 60 * 1000
export const AI_IMAGE_DEFAULT_BASE_URL = 'https://api.xnkaixin.eu.cc'

export type AIImageSizeTier = '1K' | '2K' | '4K'
export type AIImageTaskStatus = 'queued' | 'running' | 'succeeded' | 'failed'
export type AIImageErrorCode =
  | 'invalid-base-url'
  | 'insecure-http'
  | 'invalid-key'
  | 'endpoint-not-found'
  | 'rate-limited'
  | 'server-error'
  | 'timeout'
  | 'network-error'
  | 'empty-response'
  | 'invalid-response'
  | 'request-failed'
  | 'aborted'

export interface AIImagePrompt {
  id: string
  name: string
  prompt: string
  builtIn: boolean
}

export interface AIImageResult {
  url: string
  source: 'base64' | 'url'
  mimeType: string
  blob?: Blob
}

export interface AIImageTask {
  id: string
  prompt: AIImagePrompt
  status: AIImageTaskStatus
  result?: AIImageResult
  errorCode?: AIImageErrorCode
  startedAt?: number
  completedAt?: number
}

export interface AIImageEndpoint {
  baseURL: string
  requestURL: string
  host: string
  external: boolean
}

export class AIImageAPIError extends Error {
  readonly code: AIImageErrorCode
  readonly status?: number

  constructor(code: AIImageErrorCode, status?: number) {
    super(code)
    this.name = 'AIImageAPIError'
    this.code = code
    this.status = status
  }
}

interface GenerateAIImageOptions {
  baseURL: string
  apiKey: string
  prompt: string
  sizeTier: AIImageSizeTier
  referenceImages?: File[]
  signal?: AbortSignal
}

interface OpenAIImageItem {
  b64_json?: unknown
  url?: unknown
}

interface OpenAIImageResponse {
  data?: unknown
}

interface OpenAIErrorResponse {
  error?: {
    code?: unknown
    type?: unknown
  }
}

function isLocalHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase()
  return (
    normalized === 'localhost' ||
    normalized.endsWith('.localhost') ||
    normalized === '::1' ||
    normalized === '[::1]' ||
    /^127(?:\.\d{1,3}){3}$/.test(normalized)
  )
}

export function getDefaultAIImageBaseURL(): string {
  return AI_IMAGE_DEFAULT_BASE_URL
}

export function resolveAIImageEndpoint(
  value: string,
  operation: 'generations' | 'edits' = 'generations'
): AIImageEndpoint {
  const raw = value.trim()
  if (!raw) {
    throw new AIImageAPIError('invalid-base-url')
  }

  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    throw new AIImageAPIError('invalid-base-url')
  }

  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new AIImageAPIError('invalid-base-url')
  }

  if (
    typeof window !== 'undefined' &&
    window.location.protocol === 'https:' &&
    parsed.protocol === 'http:' &&
    !isLocalHostname(parsed.hostname)
  ) {
    // HTTPS 页面不得把密钥发送到远程明文 HTTP 地址，本机调试地址除外。
    throw new AIImageAPIError('insecure-http')
  }

  parsed.hash = ''
  parsed.search = ''
  // 只填写站点域名时自动补齐 OpenAI 兼容版本路径；本地开发地址也会命中 Vite 的 /v1 代理。
  if (!parsed.pathname || parsed.pathname === '/') {
    parsed.pathname = '/v1'
  }
  const baseURL = parsed.toString().replace(/\/+$/, '')
  const requestURL = `${baseURL}/images/${operation}`
  const external = typeof window !== 'undefined' && parsed.origin !== window.location.origin

  return {
    baseURL,
    requestURL,
    host: parsed.host,
    external
  }
}

async function mapHTTPError(response: Response): Promise<AIImageAPIError> {
  const { status } = response
  if (status === 401 || status === 403) return new AIImageAPIError('invalid-key', status)
  if (status === 404) return new AIImageAPIError('endpoint-not-found', status)
  if (status === 429) return new AIImageAPIError('rate-limited', status)
  if (status >= 500) return new AIImageAPIError('server-error', status)

  // 只读取错误标识用于分类，不把供应商原始消息带到页面或日志。
  try {
    const payload = (await response.json()) as OpenAIErrorResponse
    const identifier = [payload.error?.code, payload.error?.type]
      .filter((value): value is string => typeof value === 'string')
      .join(' ')
      .toLowerCase()
    if (/invalid.*key|api.*key|authentication|unauthorized/.test(identifier)) {
      return new AIImageAPIError('invalid-key', status)
    }
    if (/rate.*limit|too.*many/.test(identifier)) {
      return new AIImageAPIError('rate-limited', status)
    }
  } catch {
    // 非 JSON 错误响应继续使用 HTTP 状态兜底。
  }
  return new AIImageAPIError('request-failed', status)
}

function base64ToResult(base64: string): AIImageResult {
  try {
    const binary = window.atob(base64)
    const bytes = new Uint8Array(binary.length)
    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index)
    }
    const blob = new Blob([bytes], { type: 'image/png' })
    return {
      url: URL.createObjectURL(blob),
      source: 'base64',
      mimeType: 'image/png',
      blob
    }
  } catch {
    throw new AIImageAPIError('invalid-response')
  }
}

function urlToResult(value: string): AIImageResult | null {
  try {
    const parsed = new URL(value)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null
    return {
      url: parsed.toString(),
      source: 'url',
      mimeType: 'image/png'
    }
  } catch {
    return null
  }
}

function parseImageResult(payload: OpenAIImageResponse): AIImageResult {
  if (!Array.isArray(payload.data) || payload.data.length === 0) {
    throw new AIImageAPIError('empty-response')
  }

  for (const rawItem of payload.data) {
    if (!rawItem || typeof rawItem !== 'object') continue
    const item = rawItem as OpenAIImageItem
    if (typeof item.b64_json === 'string' && item.b64_json.trim()) {
      return base64ToResult(item.b64_json)
    }
    if (typeof item.url === 'string' && item.url.trim()) {
      const result = urlToResult(item.url)
      if (result) return result
    }
  }

  throw new AIImageAPIError('empty-response')
}

export async function generateAIImage(options: GenerateAIImageOptions): Promise<AIImageResult> {
  const referenceImages = options.referenceImages?.slice(0, 3) || []
  const endpoint = resolveAIImageEndpoint(
    options.baseURL,
    referenceImages.length ? 'edits' : 'generations'
  )
  const controller = new AbortController()
  let timedOut = false

  const handleExternalAbort = () => controller.abort()
  if (options.signal?.aborted) {
    throw new AIImageAPIError('aborted')
  }
  options.signal?.addEventListener('abort', handleExternalAbort, { once: true })

  const timeoutID = window.setTimeout(() => {
    timedOut = true
    controller.abort()
  }, AI_IMAGE_TIMEOUT_MS)

  try {
    const requestBody = referenceImages.length
      ? createEditRequestBody(options.prompt, options.sizeTier, referenceImages)
      : JSON.stringify({
          model: AI_IMAGE_MODEL,
          prompt: options.prompt,
          n: 1,
          size: options.sizeTier,
          output_format: 'png',
          response_format: 'b64_json'
        })

    const response = await fetch(endpoint.requestURL, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${options.apiKey}`,
        ...(referenceImages.length ? {} : { 'Content-Type': 'application/json' })
      },
      // 参考图请求由浏览器生成 multipart boundary，不手动覆盖 Content-Type。
      body: requestBody,
      signal: controller.signal
    })

    if (!response.ok) {
      const apiError = await mapHTTPError(response)
      if (controller.signal.aborted) {
        throw new AIImageAPIError(timedOut ? 'timeout' : 'aborted')
      }
      throw apiError
    }

    let payload: OpenAIImageResponse
    try {
      payload = (await response.json()) as OpenAIImageResponse
    } catch {
      if (controller.signal.aborted) {
        throw new AIImageAPIError(timedOut ? 'timeout' : 'aborted')
      }
      throw new AIImageAPIError('invalid-response')
    }

    return parseImageResult(payload)
  } catch (error) {
    if (error instanceof AIImageAPIError) throw error
    if (controller.signal.aborted) {
      throw new AIImageAPIError(timedOut ? 'timeout' : 'aborted')
    }
    if (error instanceof TypeError) {
      throw new AIImageAPIError('network-error')
    }
    throw new AIImageAPIError('request-failed')
  } finally {
    window.clearTimeout(timeoutID)
    options.signal?.removeEventListener('abort', handleExternalAbort)
  }
}

function createEditRequestBody(prompt: string, sizeTier: AIImageSizeTier, referenceImages: File[]): FormData {
  const body = new FormData()
  body.set('model', AI_IMAGE_MODEL)
  body.set('prompt', prompt)
  body.set('n', '1')
  body.set('size', sizeTier)
  body.set('output_format', 'png')
  body.set('response_format', 'b64_json')
  for (const image of referenceImages) {
    body.append('image', image, image.name)
  }
  return body
}

export async function getAIImageResultBlob(result: AIImageResult): Promise<Blob> {
  if (result.blob) return result.blob
  const response = await fetch(result.url)
  if (!response.ok) throw new Error('image-fetch-failed')
  return response.blob()
}

export function revokeAIImageResult(result?: AIImageResult): void {
  if (result?.source === 'base64') {
    URL.revokeObjectURL(result.url)
  }
}

function triggerDownload(url: string, filename: string): void {
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.rel = 'noopener noreferrer'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
}

export async function downloadAIImageResult(
  result: AIImageResult,
  filename: string
): Promise<'downloaded' | 'opened'> {
  if (result.blob) {
    triggerDownload(result.url, filename)
    return 'downloaded'
  }

  try {
    const response = await fetch(result.url)
    if (!response.ok) throw new Error('download-failed')
    const blobURL = URL.createObjectURL(await response.blob())
    try {
      triggerDownload(blobURL, filename)
    } finally {
      URL.revokeObjectURL(blobURL)
    }
    return 'downloaded'
  } catch {
    window.open(result.url, '_blank', 'noopener,noreferrer')
    return 'opened'
  }
}
