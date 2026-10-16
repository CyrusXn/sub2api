import { afterEach, describe, expect, it, vi } from 'vitest'
import { keysAPI } from '../keys'
import { generateWorkspaceVideo, getWorkspaceSelection, loadWorkspaceKeys, loadWorkspaceSelection, modelMatchesTab, preferredModels, streamWorkspaceChat, workspaceModelsForKey, workspaceSelectableModels, type WorkspaceKey } from '../workspace'

vi.mock('../keys', () => ({ keysAPI: { list: vi.fn() } }))

const key = (id: number, models: string[] = [], groupName = '分组'): WorkspaceKey => ({
  id, name: `Key ${id}`, key: 'test-only-key', groupName, platform: 'grok', baseUrl: 'https://relay.example/prefix/v1/', models, allowImageGeneration: true
})
afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); vi.useRealTimers() })

describe('workspace model selection', () => {
  it.each([
    ['gpt-5.6-sol', 'gpt-6.1-sol'],
    ['gemini-4-argon', 'gemini-4-argon-high'],
    ['claude-sonnet-5', 'claude-opus-5-5'],
    ['claude-sonnet-5', 'claude-opus-5.5'],
    ['grok-4', 'grok-4.7']
  ])('对话在实际模型列表中优先 %s 对应的 %s', (first, preferred) => {
    expect(preferredModels([first, preferred], 'chat')).toEqual([preferred, first])
  })

  it('未返回指定模型或未知平台时保持第一项，不编造最新模型', () => {
    expect(preferredModels(['gpt-5.6-sol', 'gpt-5.5'], 'chat')).toEqual(['gpt-5.6-sol', 'gpt-5.5'])
    expect(preferredModels(['doubao-pro', 'qwen-plus'], 'chat')).toEqual(['doubao-pro', 'qwen-plus'])
  })

  it.each(['name', 'groupName'] as const)('对话优先 %s 中标记的官 key，不被其他 Key 的模型优先级覆盖', field => {
    const first = key(1, ['gpt-6.1-sol'])
    const official = { ...key(2, ['gemini-4-argon', 'gemini-4-argon-high']), [field]: '【Gemini】真官 Key' }
    expect(getWorkspaceSelection([first, official], 'chat')).toEqual({ key: official, model: 'gemini-4-argon-high' })
  })

  it('无可用官 key 时优先第一个可对话 Key，而非跨 Key 追逐默认模型', () => {
    const media = { ...key(1, ['gpt-image-2']), name: '官key' }
    const first = key(2, ['qwen-plus', 'qwen-max'])
    expect(getWorkspaceSelection([media, first, key(3, ['gpt-6.1-sol'])], 'chat')).toEqual({ key: first, model: 'qwen-plus' })
  })

  it('保留分组生图权限，已有分组缺少授权时默认不开放', async () => {
    vi.mocked(keysAPI.list).mockResolvedValue({ items: [
      { id: 1, key: 'test-only-key', status: 'active', quota: 0, quota_used: 0, group: { platform: 'openai', allow_image_generation: true } },
      { id: 2, key: 'test-only-key', status: 'active', quota: 0, quota_used: 0, group: { platform: 'grok', allow_image_generation: false } },
      { id: 3, key: 'test-only-key', status: 'active', quota: 0, quota_used: 0, group: { platform: 'openai' } }
    ], pages: 1 } as Awaited<ReturnType<typeof keysAPI.list>>)
    expect((await loadWorkspaceKeys('https://relay.example')).map(item => item.allowImageGeneration)).toEqual([true, false, false])
  })

  it('禁用媒体生成的 Key 不参与图片或视频选择，但仍可对话', async () => {
    const blocked = { ...key(1, ['gpt-image-2', 'seedance-1-5-pro', 'gpt-5.6-sol']), platform: 'openai', allowImageGeneration: false, modelsLoaded: true }
    const allowed = { ...key(2, ['grok-imagine-image', 'grok-imagine-video'], 'SuperGrok Heavy'), modelsLoaded: true }
    expect((await loadWorkspaceSelection([blocked, allowed], 'image'))?.key.id).toBe(2)
    expect((await loadWorkspaceSelection([blocked, allowed], 'video'))?.key.id).toBe(2)
    expect((await loadWorkspaceSelection([blocked, allowed], 'chat'))?.key.id).toBe(1)
    expect(await loadWorkspaceSelection([blocked], 'image')).toBeNull()
  })

  it('图片分类只开放本网关支持的 GPT 和 Grok 图片家族', () => {
    for (const model of ['gpt-image-2', 'grok-imagine', 'grok-imagine-edit', 'grok-imagine-image']) {
      expect(modelMatchesTab(model, 'image')).toBe(true)
      expect(modelMatchesTab(model, 'chat')).toBe(false)
    }
    for (const model of ['dall-e-3', 'flux-1', 'seedream-4', 'gemini-4-image', 'grok-imagine-video']) {
      expect(modelMatchesTab(model, 'image')).toBe(false)
      expect(modelMatchesTab(model, 'chat')).toBe(false)
    }
  })

  it('TypeSafe 和原生专用 jev 模型不能阻挡可对话 Key 的匹配', async () => {
    const native = { ...key(1, ['jev-latest']), platform: 'typesafe', modelsLoaded: true }
    expect(workspaceModelsForKey(native, 'chat')).toEqual([])
    expect(modelMatchesTab('jev-latest', 'chat')).toBe(false)
    expect((await loadWorkspaceSelection([native, { ...key(2, ['gpt-5.6-sol']), platform: 'openai', modelsLoaded: true }], 'chat'))?.key.id).toBe(2)
  })

  it('prefers Seedance video models before Grok Heavy video', () => {
    expect(preferredModels(['grok-imagine-video', 'seedance-1-5-pro', 'gpt-5.6-sol'], 'video')).toEqual([
      'seedance-1-5-pro', 'grok-imagine-video'
    ])
  })

  it.each(['grok-imagine-video-1.5', 'grok-imagine-video-1.5-preview', 'grok-video-1.5', ' xai/GROK-IMAGINE-VIDEO-1.5 '])('视频工作台不将仅图生视频的 %s 识别为可执行型号', model => {
    expect(modelMatchesTab(model, 'video')).toBe(false)
    expect(preferredModels([model, 'grok-imagine-video', 'seedance-1-5-pro'], 'video')).toEqual(['seedance-1-5-pro', 'grok-imagine-video'])
  })

  it('Heavy 仅有图生视频型号时不自动匹配，也不降级到 free', () => {
    const heavy = key(1, ['grok-imagine-video-1.5', 'grok-imagine-video-1.5-preview'], '【Grok】Heavy')
    const free = key(2, ['grok-imagine-video'], '【Grok】free')
    expect(workspaceModelsForKey(heavy, 'video')).toEqual([])
    expect(getWorkspaceSelection([heavy, free], 'video')).toBeNull()
    const seedance = { ...key(3, ['seedance-1-5-pro']), platform: 'openai' }
    expect(getWorkspaceSelection([heavy, seedance], 'video')).toEqual({ key: seedance, model: 'seedance-1-5-pro' })
  })

  it('展开全部也不能重新选择工作台不支持的视频型号', () => {
    const heavy = key(1, ['grok-imagine-video-1.5', 'grok-video-1.5', 'grok-4.7'], '【Grok】Heavy')
    expect(workspaceSelectableModels(heavy, 'video', false)).toEqual([])
    expect(workspaceSelectableModels(heavy, 'video', true)).toEqual([])
    expect(workspaceSelectableModels(key(2, ['grok-imagine-video-1.5', 'grok-imagine-video']), 'video', true)).toEqual(['grok-imagine-video'])
  })

  it('keeps each tab on its matching capability', () => {
    expect(modelMatchesTab('gpt-image-2', 'image')).toBe(true)
    expect(modelMatchesTab('gpt-image-2', 'chat')).toBe(false)
    expect(modelMatchesTab('gpt-5.6-sol', 'chat')).toBe(true)
    expect(modelMatchesTab('whisper-1', 'chat')).toBe(false)
    expect(modelMatchesTab('doubao-pro', 'chat')).toBe(true)
    expect(modelMatchesTab('sora-video', 'video')).toBe(false)
    expect(modelMatchesTab('gemini-image', 'image')).toBe(false)
  })

  it('图片优先使用现有 gpt-image-2', () => {
    expect(preferredModels(['grok-imagine-image', 'gpt-image-2'], 'image')[0]).toBe('gpt-image-2')
  })

  it('跨 Key 优先选择 Seedance，而非第一个 Key 的 Grok', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ data: [{ id: 'grok-imagine-video' }] }))
      .mockResolvedValueOnce(Response.json({ data: [{ id: 'seedance-1-5-pro' }] }))
    vi.stubGlobal('fetch', fetchMock)
    const result = await loadWorkspaceSelection([key(1), { ...key(2), platform: 'openai' }], 'video')
    expect(result?.key.id).toBe(2)
    expect(result?.model).toBe('seedance-1-5-pro')
  })

  it('无 Seedance 时优先 Heavy 分组的 Grok 视频模型', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(Response.json({ data: [{ id: 'grok-imagine-video' }] }))))
    expect((await loadWorkspaceSelection([key(1), key(2, [], 'SuperGrok Heavy')], 'video'))?.key.id).toBe(2)
  })

  it('Heavy 无权限或模型加载失败时不自动降级到 free', async () => {
    const free = { ...key(1, ['grok-imagine-video'], '【Grok】free'), modelsLoaded: true }
    const heavy = { ...key(2, ['grok-imagine-video'], '【Grok】Heavy'), allowImageGeneration: false, modelsLoaded: true }
    expect(await loadWorkspaceSelection([free, heavy], 'video')).toBeNull()
    expect(workspaceModelsForKey(free, 'video')).toEqual(['grok-imagine-video'])
  })

  it('模型加载有界并发，不逐个串行等待', async () => {
    const resolvers: Array<() => void> = []
    const fetchMock = vi.fn().mockImplementation(() => new Promise(resolve => {
      resolvers.push(() => resolve(Response.json({ data: [{ id: 'grok-4.7' }] })))
    }))
    vi.stubGlobal('fetch', fetchMock)
    const pending = loadWorkspaceSelection(Array.from({ length: 6 }, (_, index) => key(index)), 'chat')
    expect(fetchMock).toHaveBeenCalledTimes(4)
    while (fetchMock.mock.calls.length < 6) {
      resolvers.splice(0).forEach(resolve => resolve())
      await new Promise(resolve => setTimeout(resolve, 0))
    }
    resolvers.splice(0).forEach(resolve => resolve())
    await pending
    expect(fetchMock).toHaveBeenCalledTimes(6)
  })

  it('模型加载失败只尝试一次，Tab 切换不自动重试', async () => {
    const keys = [key(1), key(2)]
    const fetchMock = vi.fn().mockRejectedValue(new Error('unavailable'))
    vi.stubGlobal('fetch', fetchMock)
    await loadWorkspaceSelection(keys, 'chat')
    await loadWorkspaceSelection(keys, 'image')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('加载取消后不继续尝试其他 Key', async () => {
    const abort = new AbortController()
    const fetchMock = vi.fn().mockImplementation(() => { abort.abort(); throw new DOMException('aborted', 'AbortError') })
    vi.stubGlobal('fetch', fetchMock)
    await expect(loadWorkspaceSelection([key(1), key(2)], 'chat', abort.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('单个模型请求超时后继续其他 Key，并清理定时器且不自动重试', async () => {
    vi.useFakeTimers()
    const keys = Array.from({ length: 5 }, (_, index) => key(index, [], '【Grok】Heavy'))
    let stalledSignal: AbortSignal | undefined
    const fetchMock = vi.fn().mockImplementationOnce((_url, init) => new Promise((_resolve, reject) => {
      stalledSignal = init.signal
      stalledSignal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true })
    })).mockImplementation(() => Promise.resolve(Response.json({ data: [{ id: 'grok-imagine-video' }] })))
    vi.stubGlobal('fetch', fetchMock)
    const pending = loadWorkspaceSelection(keys, 'video')
    await vi.advanceTimersByTimeAsync(7999)
    expect(stalledSignal?.aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    expect((await pending)?.key.id).toBe(1)
    expect(stalledSignal?.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    await loadWorkspaceSelection(keys, 'chat')
    expect(fetchMock).toHaveBeenCalledTimes(5)
  })

  it('工作台 Gemini 自动使用截图中的 generateContent 协议', async () => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json({ candidates: [{ content: { parts: [{ text: '内部推理', thought: true }, { text: '你好世界', thoughtSignature: 'test-signature' }] }, finishReason: 'STOP' }] }))
    vi.stubGlobal('fetch', fetchMock)
    const onText = vi.fn()
    await streamWorkspaceChat({ selection: { key: { ...key(1), platform: 'gemini' }, model: 'gemini-4-argon' },
      messages: [{ role: 'user', content: '你好' }, { role: 'assistant', content: '您好' }, { role: 'user', content: '继续' }], signal: new AbortController().signal, onText })
    expect(fetchMock).toHaveBeenCalledWith('https://relay.example/prefix/v1beta/models/gemini-4-argon:generateContent', expect.objectContaining({
      method: 'POST', credentials: 'omit', headers: expect.objectContaining({ 'x-goog-api-key': 'test-only-key' }),
      body: JSON.stringify({ contents: [
        { role: 'user', parts: [{ text: '你好' }] }, { role: 'model', parts: [{ text: '您好' }] }, { role: 'user', parts: [{ text: '继续' }] }
      ], generationConfig: { maxOutputTokens: 2048 } })
    }))
    expect(onText).toHaveBeenCalledWith('你好世界')
  })

  it('与密钥页一致：Gemini 分组内的非 Gemini 映射模型使用 OpenAI 兼容协议', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('data: {"choices":[{"delta":{"content":"你好"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n', { headers: { 'Content-Type': 'text/event-stream' } }))
    vi.stubGlobal('fetch', fetchMock)
    await streamWorkspaceChat({ selection: { key: { ...key(1), platform: 'gemini' }, model: 'gpt-6.1-sol' }, messages: [{ role: 'user', content: '你是谁' }], signal: new AbortController().signal, onText: vi.fn() })
    expect(fetchMock).toHaveBeenCalledWith('https://relay.example/prefix/v1/chat/completions', expect.objectContaining({ headers: expect.objectContaining({ Authorization: 'Bearer test-only-key' }) }))
  })

  it('Seedance 提交选定参数，成功结果不请求不存在的 content 路由', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ id: 'task-1' }))
      .mockResolvedValueOnce(Response.json({ status: 'succeeded', content: { video_url: 'https://cdn.example/video.mp4' } }))
    vi.stubGlobal('fetch', fetchMock)
    const result = await generateWorkspaceVideo({ selection: { key: key(1), model: 'seedance-1-5-pro' }, prompt: 'waves', duration: 6, aspectRatio: '9:16', resolution: '720p', signal: new AbortController().signal })
    expect(fetchMock).toHaveBeenNthCalledWith(1, 'https://relay.example/prefix/api/v3/contents/generations/tasks', expect.objectContaining({
      body: JSON.stringify({ model: 'seedance-1-5-pro', content: [{ type: 'text', text: 'waves' }], duration: 6, ratio: '9:16', resolution: '720p' })
    }))
    expect(result.url).toBe('https://cdn.example/video.mp4')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it.each(['grok-imagine-video-1.5', 'grok-imagine-video-1.5-preview', 'grok-video-1.5', 'grok-4.7'])('%s 在请求前拒绝工作台不支持的视频型号，参考视频不能替代输入图片', async model => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json({}))
    vi.stubGlobal('fetch', fetchMock)
    for (const referenceVideoURL of [undefined, 'https://cdn.example/reference.mp4']) {
      await expect(generateWorkspaceVideo({ selection: { key: key(1), model }, prompt: 'waves', referenceVideoURL,
        duration: 6, aspectRatio: '16:9', resolution: '720p', signal: new AbortController().signal })).rejects.toThrow('video-model-unsupported')
    }
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('Seedance 参考视频使用原生 video_url 和 reference_video，保留生成参数', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ id: 'task-reference' }))
      .mockResolvedValueOnce(Response.json({ status: 'succeeded', content: { video_url: 'https://cdn.example/result.mp4' } }))
    vi.stubGlobal('fetch', fetchMock)
    const options = { selection: { key: key(1), model: 'doubao-seedance-2-0-260128' }, prompt: '参考视频中的运镜',
      referenceVideoURL: '  https://cdn.example/reference.mp4?signature=test-only  ', duration: 6, aspectRatio: '9:16', resolution: '720p', signal: new AbortController().signal }
    expect(await generateWorkspaceVideo(options)).toEqual({ url: 'https://cdn.example/result.mp4', mimeType: 'video/mp4' })
    expect(fetchMock).toHaveBeenNthCalledWith(1, 'https://relay.example/prefix/api/v3/contents/generations/tasks', expect.objectContaining({
      method: 'POST', credentials: 'omit', redirect: 'error', headers: { Authorization: 'Bearer test-only-key', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'doubao-seedance-2-0-260128', content: [
        { type: 'text', text: '参考视频中的运镜' },
        { type: 'video_url', video_url: { url: 'https://cdn.example/reference.mp4?signature=test-only' }, role: 'reference_video' }
      ], duration: 6, ratio: '9:16', resolution: '720p' })
    }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it.each(['seedance-1-5-pro', 'doubao-seedance-1-0-pro-250528'])('明确不支持视频输入的 %s 在请求前拒绝参考视频', async model => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json({}))
    vi.stubGlobal('fetch', fetchMock)
    const options = { selection: { key: key(1), model }, prompt: '参考视频', referenceVideoURL: 'https://cdn.example/reference.mp4',
      duration: 6, aspectRatio: '16:9', resolution: '720p', signal: new AbortController().signal }
    await expect(generateWorkspaceVideo(options)).rejects.toThrow('video-reference-unsupported')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it.each(['grok-imagine-video', 'doubao-seedance-2-0-260128'])('%s 不向上游发送无效或携带凭据的参考地址', async model => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json({}))
    vi.stubGlobal('fetch', fetchMock)
    for (const referenceVideoURL of ['http://cdn.example/reference.mp4', 'javascript:alert(1)', 'data:video/mp4;base64,AAAA',
      'blob:https://app.example/video', '/reference.mp4', 'not a URL', 'https://', 'https://user:password@cdn.example/reference.mp4',
      'https://cdn.example/ref\nerence.mp4']) {
      const options = { selection: { key: key(1), model }, prompt: '参考视频', referenceVideoURL,
        duration: 6, aspectRatio: '16:9', resolution: '720p', signal: new AbortController().signal }
      await expect(generateWorkspaceVideo(options)).rejects.toThrow('video-invalid-reference-url')
    }
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('Seedance 无输出 URL 时明确报错，而非猜测 content 接口', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ id: 'task-1' }))
      .mockResolvedValueOnce(Response.json({ status: 'succeeded' }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(generateWorkspaceVideo({ selection: { key: key(1), model: 'seedance-1-5-pro' }, prompt: 'waves', duration: 6, aspectRatio: '16:9', resolution: '720p', signal: new AbortController().signal })).rejects.toThrow('video-content-missing')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('Grok 完成后经鉴权 content 接口下载，任务 ID 正确编码', async () => {
    const createObjectURL = vi.fn().mockReturnValue('blob:workspace-video')
    vi.stubGlobal('URL', Object.assign(class extends URL {}, { createObjectURL }))
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ request_id: 'task/1' }))
      .mockResolvedValueOnce(Response.json({ status: 'done', video: { url: 'https://cdn.example/video.mp4' } }))
      .mockResolvedValueOnce(new Response('video', { headers: { 'Content-Type': 'video/mp4' } }))
    vi.stubGlobal('fetch', fetchMock)
    const result = await generateWorkspaceVideo({ selection: { key: key(1), model: 'grok-imagine-video' }, prompt: 'waves', duration: 6, aspectRatio: '9:16', resolution: '720p', signal: new AbortController().signal })
    expect(fetchMock).toHaveBeenNthCalledWith(1, 'https://relay.example/prefix/v1/videos/generations', expect.objectContaining({
      body: JSON.stringify({ model: 'grok-imagine-video', prompt: 'waves', duration: 6, aspect_ratio: '9:16', resolution: '720p' })
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(3, 'https://relay.example/prefix/v1/videos/task%2F1/content', expect.objectContaining({
      credentials: 'omit', redirect: 'error', headers: { Authorization: 'Bearer test-only-key' }
    }))
    expect(result).toEqual({ url: 'blob:workspace-video', mimeType: 'video/mp4' })
    expect(createObjectURL).toHaveBeenCalledWith(expect.objectContaining({ type: 'video/mp4', size: 5 }))
  })

  it('Grok 参考视频走编辑接口且不发送源片继承的规格，复用原鉴权轮询下载', async () => {
    vi.stubGlobal('URL', Object.assign(class extends URL {}, { createObjectURL: vi.fn().mockReturnValue('blob:workspace-video-edit') }))
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ request_id: 'edit/1' }))
      .mockResolvedValueOnce(Response.json({ status: 'done' }))
      .mockResolvedValueOnce(new Response('video', { headers: { 'Content-Type': 'video/mp4' } }))
    vi.stubGlobal('fetch', fetchMock)
    const options = { selection: { key: key(1), model: 'grok-imagine-video' }, prompt: '改成冬天', referenceVideoURL: 'https://cdn.example/reference.mp4',
      duration: 0, aspectRatio: '9:16', resolution: '720p', signal: new AbortController().signal }
    expect(await generateWorkspaceVideo(options)).toEqual({ url: 'blob:workspace-video-edit', mimeType: 'video/mp4' })
    expect(fetchMock).toHaveBeenNthCalledWith(1, 'https://relay.example/prefix/v1/videos/edits', expect.objectContaining({
      method: 'POST', credentials: 'omit', redirect: 'error', headers: { Authorization: 'Bearer test-only-key', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'grok-imagine-video', prompt: '改成冬天', video: { url: 'https://cdn.example/reference.mp4' } })
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(2, 'https://relay.example/prefix/v1/videos/edit%2F1', expect.objectContaining({ headers: { Authorization: 'Bearer test-only-key' } }))
    expect(fetchMock).toHaveBeenNthCalledWith(3, 'https://relay.example/prefix/v1/videos/edit%2F1/content', expect.objectContaining({ headers: { Authorization: 'Bearer test-only-key' } }))
  })

  it('空白参考视频按原 Grok 文生视频处理', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ request_id: 'task-1' }))
      .mockResolvedValueOnce(Response.json({ status: 'failed' }))
    vi.stubGlobal('fetch', fetchMock)
    const options = { selection: { key: key(1), model: 'grok-imagine-video' }, prompt: 'waves', referenceVideoURL: '  ',
      duration: 6, aspectRatio: '16:9', resolution: '720p', signal: new AbortController().signal }
    await expect(generateWorkspaceVideo(options)).rejects.toThrow('video-generation-failed')
    expect(fetchMock).toHaveBeenNthCalledWith(1, 'https://relay.example/prefix/v1/videos/generations', expect.objectContaining({
      body: JSON.stringify({ model: 'grok-imagine-video', prompt: 'waves', duration: 6, aspect_ratio: '16:9', resolution: '720p' })
    }))
  })

  it('失败的视频任务立即停止，不继续轮询或下载', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ request_id: 'task-1' }))
      .mockResolvedValueOnce(Response.json({ status: 'failed' }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(generateWorkspaceVideo({ selection: { key: key(1), model: 'grok-imagine-video' }, prompt: 'waves', duration: 6, aspectRatio: '16:9', resolution: '720p', signal: new AbortController().signal })).rejects.toThrow('video-generation-failed')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('停止等待清理轮询定时器，不再请求任务状态', async () => {
    vi.useFakeTimers()
    const controller = new AbortController()
    const fetchMock = vi.fn().mockResolvedValueOnce(Response.json({ request_id: 'task-1' }))
      .mockResolvedValue(Response.json({ status: 'pending' }))
    vi.stubGlobal('fetch', fetchMock)
    const pending = generateWorkspaceVideo({ selection: { key: key(1), model: 'grok-imagine-video' }, prompt: 'waves', duration: 6, aspectRatio: '16:9', resolution: '720p', signal: controller.signal })
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    await vi.advanceTimersByTimeAsync(0)
    expect(vi.getTimerCount()).toBe(1)
    controller.abort()
    await rejected
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(6000)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
