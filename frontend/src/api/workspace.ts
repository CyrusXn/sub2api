import { keysAPI } from './keys'
import type { ApiKey, PaginatedResponse } from '@/types'
import { fetchKeyTestModels, streamKeyTest, type TestMessage } from './keyTest'

export type WorkspaceTab = 'chat' | 'image' | 'video'

export interface WorkspaceKey {
  id: number
  name: string
  key: string
  groupName: string
  platform: string
  baseUrl: string
  models: string[]
  allowImageGeneration: boolean
  modelsLoaded?: boolean
  modelsLoadFailed?: boolean
}

export interface WorkspaceSelection {
  key: WorkspaceKey
  model: string
}

const NON_CHAT_HINTS = ['image', 'dall-e', 'flux', 'seedream', 'grok-imagine', 'video', 'seedance', 'whisper', 'tts', 'audio', 'embedding', 'rerank', 'moderation', 'imagen', 'veo', 'jev-']

function isSeedanceModel(model: string): boolean {
  return /seedance|doubao.*video/i.test(model)
}

function isUsableKey(key: ApiKey): boolean {
  return key.status === 'active' && (!key.expires_at || new Date(key.expires_at).getTime() > Date.now()) &&
    (key.quota <= 0 || key.quota_used < key.quota) && key.group?.status !== 'inactive' && Boolean(key.key && !key.key.includes('...'))
}

export function modelMatchesTab(model: string, tab: WorkspaceTab): boolean {
  const normalized = model.trim().toLowerCase()
  if (tab === 'chat') return Boolean(normalized.trim()) && !NON_CHAT_HINTS.some((hint) => normalized.includes(hint))
  // 本期生图复用 OpenAI Images；Gemini 图片端点不伪装为兼容接口。
  if (tab === 'image') return normalized.startsWith('gpt-image-') || normalized === 'grok-imagine' || normalized === 'grok-imagine-edit' || normalized.startsWith('grok-imagine-image')
  // Grok 1.5 仅支持图生视频；当前工作台没有输入图片协议，不能视为可执行型号。
  return isSeedanceModel(model) || (/grok.*video/i.test(model) && !/grok-(?:imagine-)?video-1\.5/i.test(model))
}

function modelPriority(model: string, tab: WorkspaceTab): number {
  if (tab === 'video') return isSeedanceModel(model) ? 0 : 1
  if (tab === 'image') return model === 'gpt-image-2' ? 0 : 1
  return /^(?:gpt-6\.1-sol|gemini-4-argon-high|claude-opus-5[.-]5|grok-4\.7)$/i.test(model) ? 0 : 1
}

export function preferredModels(models: string[], tab: WorkspaceTab): string[] {
  return models.filter((model) => modelMatchesTab(model, tab)).sort((a, b) => modelPriority(a, tab) - modelPriority(b, tab))
}

export function workspaceModelsForKey(key: WorkspaceKey, tab: WorkspaceTab): string[] {
  // 图片与视频创建共享后端分组授权；TypeSafe 仅支持本期未接入的 System One。
  if ((tab !== 'chat' && !key.allowImageGeneration) || (tab === 'chat' && key.platform === 'typesafe')) return []
  return preferredModels(key.models, tab).filter(model => {
    if (tab === 'image') return ['openai', 'grok', 'composite'].includes(key.platform)
    if (tab === 'video') return (isSeedanceModel(model) ? ['openai', 'composite'] : ['grok', 'composite']).includes(key.platform)
    return true
  })
}

export function workspaceSelectableModels(key: WorkspaceKey, tab: WorkspaceTab, showAll: boolean): string[] {
  const matched = workspaceModelsForKey(key, tab)
  // 展开全部不放开视频输入协议限制；跨分组权限仍由网关最终校验。
  return showAll && !matched.length ? key.models.filter(model => tab !== 'video' || modelMatchesTab(model, tab)) : matched
}

export async function loadWorkspaceKeys(baseUrl: string, signal?: AbortSignal): Promise<WorkspaceKey[]> {
  const first = await keysAPI.list(1, 100, { status: 'active' }, { signal })
  const pages = await Promise.all(Array.from({ length: Math.max(0, first.pages - 1) }, (_, index) =>
    keysAPI.list(index + 2, 100, { status: 'active' }, { signal })))
  const keys = [first, ...pages].flatMap((page: PaginatedResponse<ApiKey>) => page.items).filter(isUsableKey)
  return keys.map((key) => ({
    id: key.id,
    name: key.name,
    key: key.key,
    groupName: key.group?.name || 'API Key',
    platform: key.group?.platform || 'unknown',
    baseUrl,
    allowImageGeneration: key.group ? key.group.allow_image_generation === true : true,
    models: []
  }))
}

export async function loadWorkspaceModels(keys: WorkspaceKey[], signal?: AbortSignal): Promise<void> {
  const pending = keys.filter(key => !key.modelsLoaded)
  let next = 0
  // 最多同时加载 4 个 Key，避免逐个等待，也避免一次向网关发出过多请求。
  await Promise.all(Array.from({ length: Math.min(4, pending.length) }, async () => {
    while (next < pending.length) {
      signal?.throwIfAborted()
      const key = pending[next++]
      const abort = new AbortController()
      const onAbort = () => abort.abort(signal?.reason)
      signal?.addEventListener('abort', onAbort, { once: true })
      const timeout = window.setTimeout(() => abort.abort(), 8000)
      try {
        key.models = await fetchKeyTestModels(key.baseUrl, key.key, abort.signal)
        key.modelsLoadFailed = false
      } catch {
        signal?.throwIfAborted()
        // 单个 Key 失败或超时不阻塞其他 Key，也不在切 Tab 时反复重试。
        key.models = []
        key.modelsLoadFailed = true
      } finally {
        window.clearTimeout(timeout)
        signal?.removeEventListener('abort', onAbort)
      }
      key.modelsLoaded = true
    }
  }))
  signal?.throwIfAborted()
}

export function getWorkspaceSelection(keys: WorkspaceKey[], tab: WorkspaceTab): WorkspaceSelection | null {
  const candidates = keys.flatMap(key => workspaceModelsForKey(key, tab).map(model => ({ key, model })))
  const isOfficial = (key: WorkspaceKey) => /官\s*key/i.test(key.name) || /官\s*key/i.test(key.groupName)
  // 对话先选官 Key，再选该 Key 的偏好模型；没有官 Key 时保留接口的 Key 顺序。
  if (tab === 'chat') return candidates.find(item => isOfficial(item.key)) || candidates[0] || null
  // 视频默认只匹配 Seedance 或 Heavy；其他 Grok 分组仍可手动选择，不悄悄降级到 free。
  const defaults = tab === 'video' ? candidates.filter(item => isSeedanceModel(item.model) || /heavy/i.test(item.key.groupName)) : candidates
  return defaults.sort((a, b) => modelPriority(a.model, tab) - modelPriority(b.model, tab) || Number(isOfficial(b.key)) - Number(isOfficial(a.key)))[0] || null
}

export async function loadWorkspaceSelection(keys: WorkspaceKey[], tab: WorkspaceTab, signal?: AbortSignal): Promise<WorkspaceSelection | null> {
  await loadWorkspaceModels(keys, signal)
  return getWorkspaceSelection(keys, tab)
}

export function streamWorkspaceChat(options: {
  selection: WorkspaceSelection
  messages: TestMessage[]
  signal: AbortSignal
  onText: (text: string) => void
}): Promise<void> {
  return streamKeyTest({
    baseUrl: options.selection.key.baseUrl,
    apiKey: options.selection.key.key,
    model: options.selection.model,
    protocol: /^gemini-/i.test(options.selection.model) ? 'gemini' : 'openai',
    messages: options.messages,
    signal: options.signal,
    onText: options.onText
  })
}

export interface VideoResult {
  url: string
  mimeType: string
}

export function workspaceGatewayURL(baseUrl: string, path: string): string {
  const base = (baseUrl.trim() || window.location.origin).replace(/\/+$/, '').replace(/\/v1(?:beta)?$/i, '')
  const devTarget = import.meta.env.VITE_DEV_PROXY_TARGET?.trim().replace(/\/+$/, '')
  if (import.meta.env.DEV && devTarget === base) return path
  return `${base}${path}`
}

interface VideoPayload {
  id?: string
  request_id?: string
  status?: string
  state?: string
  video?: { url?: string }
  content?: { video_url?: string }
  video_url?: string
  url?: string
}

async function readJSON(response: Response): Promise<VideoPayload> {
  if (!response.ok) throw new Error('video-request-failed')
  const payload: unknown = await response.json().catch(() => null)
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('video-invalid-response')
  return payload as VideoPayload
}

export function validateWorkspaceReferenceVideoURL(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return ''
  try {
    const url = new URL(trimmed)
    if (!/^https:\/\/\S+$/i.test(trimmed) || url.protocol !== 'https:' || url.username || url.password) throw new Error()
    return trimmed
  } catch {
    throw new Error('video-invalid-reference-url')
  }
}

export async function generateWorkspaceVideo(options: {
  selection: WorkspaceSelection
  prompt: string
  referenceVideoURL?: string
  duration: number
  aspectRatio: string
  resolution: string
  signal: AbortSignal
  onStatus?: (status: string) => void
}): Promise<VideoResult> {
  const { selection } = options
  options.signal.throwIfAborted()
  if (!modelMatchesTab(selection.model, 'video')) throw new Error('video-model-unsupported')
  const isSeedance = isSeedanceModel(selection.model)
  const referenceVideoURL = validateWorkspaceReferenceVideoURL(options.referenceVideoURL || '')
  if (referenceVideoURL && isSeedance && /seedance[-_.]?1(?:[-_.]|$)/i.test(selection.model)) throw new Error('video-reference-unsupported')
  const isGrokEdit = !isSeedance && Boolean(referenceVideoURL)
  if (!isGrokEdit && (!Number.isInteger(options.duration) || options.duration < 1 || options.duration > 15)) throw new Error('video-invalid-duration')
  const root = isSeedance ? '/api/v3/contents/generations/tasks' : isGrokEdit ? '/v1/videos/edits' : '/v1/videos/generations'
  const body = isSeedance
    ? { model: selection.model, content: [
      { type: 'text', text: options.prompt },
      ...(referenceVideoURL ? [{ type: 'video_url', video_url: { url: referenceVideoURL }, role: 'reference_video' }] : [])
    ], duration: options.duration, ratio: options.aspectRatio, resolution: options.resolution }
    // Grok 编辑沿用源片规格，官方接口不接受文生视频的时长、比例和分辨率。
    : isGrokEdit ? { model: selection.model, prompt: options.prompt, video: { url: referenceVideoURL } }
      : { model: selection.model, prompt: options.prompt, duration: options.duration, aspect_ratio: options.aspectRatio, resolution: options.resolution }
  const create = await readJSON(await fetch(workspaceGatewayURL(selection.key.baseUrl, root), {
    method: 'POST', headers: { Authorization: `Bearer ${selection.key.key}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(body), signal: options.signal, credentials: 'omit', redirect: 'error'
  }))
  const id = String(create.id || create.request_id || '').trim()
  if (!id) throw new Error('video-request-missing-id')
  const statusPath = isSeedance ? `/api/v3/contents/generations/tasks/${encodeURIComponent(id)}` : `/v1/videos/${encodeURIComponent(id)}`
  for (let attempt = 0; attempt < 100; attempt += 1) {
    options.signal.throwIfAborted()
    const status = await readJSON(await fetch(workspaceGatewayURL(selection.key.baseUrl, statusPath), {
      headers: { Authorization: `Bearer ${selection.key.key}` }, signal: options.signal, credentials: 'omit', redirect: 'error'
    }))
    const state = String(status.status || status.state || '').toLowerCase()
    options.onStatus?.(state || 'processing')
    if (['failed', 'error', 'canceled', 'cancelled'].includes(state)) throw new Error('video-generation-failed')
    if (['done', 'completed', 'succeeded', 'success'].includes(state)) {
      if (isSeedance) {
        const directURL = status.content?.video_url
        if (typeof directURL !== 'string' || !/^https:\/\//i.test(directURL)) throw new Error('video-content-missing')
        return { url: directURL, mimeType: 'video/mp4' }
      }
      const contentPath = `/v1/videos/${encodeURIComponent(id)}/content`
      const contentResponse = await fetch(workspaceGatewayURL(selection.key.baseUrl, contentPath), { headers: { Authorization: `Bearer ${selection.key.key}` }, signal: options.signal, credentials: 'omit', redirect: 'error' })
      if (!contentResponse.ok) throw new Error('video-content-failed')
      return { url: URL.createObjectURL(await contentResponse.blob()), mimeType: contentResponse.headers.get('content-type') || 'video/mp4' }
    }
    await new Promise<void>((resolve, reject) => {
      options.signal.throwIfAborted()
      let timer = 0
      const onAbort = () => { window.clearTimeout(timer); reject(options.signal.reason || new Error('aborted')) }
      timer = window.setTimeout(() => { options.signal.removeEventListener('abort', onAbort); resolve() }, 3000)
      options.signal.addEventListener('abort', onAbort, { once: true })
    })
  }
  throw new Error('video-generation-timeout')
}
