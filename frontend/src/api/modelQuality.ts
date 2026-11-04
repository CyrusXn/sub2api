import DOMPurify from 'dompurify'
import { apiClient } from './client'

export type QualityTheme = 'pelican' | 'candy' | 'official' | 'custom'
export type QualityStatus = 'running' | 'completed' | 'failed'
export interface QualityRun {
  id: number
  author: string
  group_name: string
  model: string
  theme: QualityTheme
  prompt: string
  status: QualityStatus
  content: string
  error?: string
  account_id?: number
  account_name?: string
  created_at: string
}
export interface QualityPage { items: QualityRun[]; total: number; page: number; page_size: number }
export interface OfficialQualityConfig { enabled: boolean; model: string; api_key_id?: number }
export interface QualityProgress { content: string; status: QualityStatus; request_id?: string }
export function modelQualityPrompt(theme: QualityTheme, custom: string): string {
  if (theme === 'official') return '你是谁'
  if (theme === 'candy') return `不使用任何外部工具回答以下问题：
在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）
          苹果味  桃子味  西瓜味
圆形         7      9      8
五角星形     7      6      4`
  const instruction = '不使用任何外部工具。请创建一个完整的 HTML，使用内联 SVG 和 CSS 动画绘制以下场景。要求画面细腻、动作自然，只返回 HTML 代码；不要外部资源、JavaScript、链接、表单或嵌入页面。场景：'
  return instruction + (theme === 'pelican' ? '一只鹈鹕骑着自行车，双脚踩动踏板，车轮转动，沿着海边前行。' : custom.trim())
}
export const modelQualityAPI = {
  async list(params: {page: number; theme: string; status: string}, signal?: AbortSignal): Promise<QualityPage> {
    const { data } = await apiClient.get('/model-quality', { params, signal })
    // 旧版服务可能回落到前端 HTML，不能把 200 当作新接口已经上线。
    if (!data || !Array.isArray(data.items) || typeof data.total !== 'number') throw { status: 404 }
    return data
  },
  async save(input: {api_key_id: number; model: string; theme: QualityTheme; prompt: string; content: string; status: QualityStatus; request_id?: string}): Promise<QualityRun> {
    const { data } = await apiClient.post<QualityRun>('/model-quality', input)
    return data
  },
  async update(id: number, progress: QualityProgress): Promise<void> {
    await apiClient.patch('/model-quality/' + id, progress)
  },
  async official(): Promise<OfficialQualityConfig> {
    const { data } = await apiClient.get('/model-quality/official')
    if (typeof data?.enabled !== 'boolean') throw { status: 404 }
    return data
  },
  async configureOfficial(input: OfficialQualityConfig): Promise<OfficialQualityConfig> {
    const { data } = await apiClient.put('/model-quality/official', input)
    return data
  }
}

export function safeModelQualityHTML(content: string): string {
  const clean = DOMPurify.sanitize(content, {
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    ADD_TAGS: ['use'],
    FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'foreignObject', 'base', 'meta', 'link', 'form', 'input', 'button', 'a', 'audio', 'video'],
    FORBID_ATTR: ['src', 'srcset', 'action', 'formaction', 'target'],
    RETURN_DOM_FRAGMENT: true
  })
  // SVG 的本地 defs/use 引用不触发网络，必须保留；其它 URL 一律移除。
  for (const node of clean.querySelectorAll('*')) {
    for (const name of node.getAttributeNames()) {
      if (name === 'href' || name === 'xlink:href') {
        const local = node.namespaceURI === 'http://www.w3.org/2000/svg' && /^#[A-Za-z_][A-Za-z0-9_:.-]*$/.test(node.getAttribute(name) || '')
        if (!local) node.removeAttribute(name)
      }
    }
  }
  const container = document.createElement('div')
  container.append(clean)
  const policy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; script-src 'none'; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'"
  return '<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="' + policy + '"><meta name="referrer" content="no-referrer"><style>html,body{margin:0;min-height:100%;overflow:hidden}svg{max-width:100%;height:auto}</style></head><body>' + container.innerHTML + '</body></html>'
}
