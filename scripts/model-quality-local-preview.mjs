#!/usr/bin/env node
// 仅用于本地视觉和流式验收：复用真实页面，所有数据及模型响应均为模拟，不代理生产请求。
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../frontend')
const { createServer } = await import(resolve(root, 'node_modules/vite/dist/node/index.js'))
const { default: vue } = await import(resolve(root, 'node_modules/@vitejs/plugin-vue/dist/index.mjs'))
const port = Number(process.env.MODEL_QUALITY_PREVIEW_PORT || 18091)
const candy = String.raw`# 本地模拟回答

**最少取出 21 颗糖果。**

| 味道 | 圆形 | 五角星形 |
| --- | ---: | ---: |
| 苹果味 | 7 | 7 |
| 桃子味 | 9 | 6 |
| 西瓜味 | 8 | 4 |

最终答案：

\[\boxed{21\text{颗}}\]

这只是用于验证排版与逐段渲染的模拟响应，不代表真实模型的检测结论。`
const identity = '**本地模拟回答**\n\n我是用于验收的模拟模型。这条记录用于检查官 key 列表样式，不会调用你的真实密钥。'
const picture = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 250"><rect width="600" height="250" fill="#e7f3ee"/><path d="M0 210H600" stroke="#7caa99"/><g fill="none" stroke="#245f54" stroke-width="5"><circle cx="205" cy="180" r="35"/><circle cx="360" cy="180" r="35"/><path d="M205 180l60-70 35 70h-95l40-85m55 85 50-85 10 85m-25-85h35"/></g><ellipse cx="282" cy="90" rx="38" ry="20" fill="white"/><path d="M300 90V54q0-16 18-16l35 14-35 7" fill="#fff" stroke="#789a8e" stroke-width="3"/><circle cx="319" cy="46" r="3"/><path d="M320 52l60 7-60 6" fill="#d8a55b"/><text x="20" y="30" font-size="14" fill="#50756b">本地模拟 SVG · 非真实模型作品</text></svg>'
let sequence = 3
let official = { enabled: false, model: '', api_key_id: 7 }
const rows = [
  { id: 3, author: 'ad***@example.com', group_name: '【GPT】真官key · 模拟', model: 'gpt-local-preview', theme: 'official', prompt: 'hi', status: 'completed', content: identity, created_at: new Date().toISOString(), account_id: 1, account_name: '本地模拟上游账号' },
  { id: 2, author: '10023***@qq.com', group_name: '优选分组 · 模拟', model: 'gemini-local-preview', theme: 'candy', prompt: '本地模拟糖果题', status: 'completed', content: candy, created_at: new Date(Date.now()-3600000).toISOString(), account_id: 2, account_name: '本地模拟账号 2' },
  { id: 1, author: '测试***用户', group_name: '绘图分组 · 模拟', model: 'gpt-local-preview', theme: 'pelican', prompt: '让鹈鹕骑上自行车，用 SVG 和 CSS 动画', status: 'completed', content: picture, created_at: new Date(Date.now()-7200000).toISOString() }
]
const virtual = {
  '@/stores/auth': 'auth', '@/api/workspace': 'workspace', '@/components/layout/AppLayout.vue': 'layout'
}
const modules = {
  auth: "import {reactive} from 'vue'; const auth=reactive({isAdmin:new URLSearchParams(location.search).get('role')!=='user'}); export const useAuthStore=()=>auth;",
  workspace: "const keys=[{id:7,name:'官key',groupName:'【GPT】真官key · 模拟',key:'local-preview-not-a-real-key',baseUrl:location.origin,platform:'openai',models:['gpt-local-preview','gemini-local-preview'],allowImageGeneration:false}]; export const loadWorkspaceKeys=async()=>keys; export const loadWorkspaceModels=async()=>{}; export const workspaceModelsForKey=(key)=>key.models;",
  layout: "import {h} from 'vue';export default {setup(_,ctx){return()=>h('div',{style:'padding:24px;max-width:1440px;margin:auto'},ctx.slots.default?.())}};",
  entry: "import {createApp} from 'vue';import Page from '/src/views/user/ModelQualityView.vue';import i18n,{initI18n} from '/src/i18n/index.ts';import '/src/style.css';await initI18n();createApp(Page).use(i18n).mount('#app');"
}
const html = '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>模型智检 · 本地模拟验收</title><body><aside style="padding:14px 24px;background:#ffefc9;color:#725117;font:13px/1.7 system-ui"><strong>本地模拟验收</strong> · 数据、密钥和回答均为模拟；不连接生产、不真实计费、不开启线上轮询。 <a href="/?role=admin">管理员视角</a> / <a href="/?role=user">用户视角</a></aside><div id="app"></div><script type="module" src="/preview-entry.js"></script></body></html>'
function json(res, data, status = 200) { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ code: status === 200 ? 0 : status, data })) }
async function body(req) { let text = ''; for await (const part of req) { text += part; if (text.length > 600000) throw Error('too large') }; return JSON.parse(text || '{}') }
async function handle(req, res, next) {
  const url = new URL(req.url, 'http://127.0.0.1')
  const path = url.pathname
  if (path === '/' || path === '/model-quality') { res.setHeader('Content-Type', 'text/html'); res.end(await server.transformIndexHtml(path, html)); return }
  if (!path.startsWith('/api') && !path.startsWith('/v1')) { next(); return }
  if (path === '/api/v1/model-quality/official') {
    if (req.method === 'PUT') official = { ...official, ...await body(req) }
    json(res, official); return
  }
  if (path === '/api/v1/model-quality' && req.method === 'GET') {
    const filtered = rows.filter(row => (!url.searchParams.get('theme') || row.theme === url.searchParams.get('theme')) && (!url.searchParams.get('status') || row.status === url.searchParams.get('status')))
    const page = Number(url.searchParams.get('page') || 1)
    json(res, { items: filtered.slice((page-1)*12, page*12), total: filtered.length, page, page_size: 12 }); return
  }
  if (path === '/api/v1/model-quality' && req.method === 'POST') {
    const input = await body(req)
    const row = { ...input, id: ++sequence, author: 'ad***@example.com', group_name: '【GPT】真官key · 模拟', account_name: '本地模拟上游账号', created_at: new Date().toISOString() }
    rows.unshift(row); json(res, row); return
  }
  if (/^\/api\/v1\/model-quality\/\d+$/.test(path) && req.method === 'PATCH') {
    const row = rows.find(item => item.id === Number(path.split('/').pop()))
    if (row) Object.assign(row, await body(req))
    json(res, { updated: Boolean(row) }); return
  }
  if ((path === '/v1/chat/completions' || path.includes(':streamGenerateContent')) && req.method === 'POST') {
    const input = await body(req)
    const prompt = input.messages?.[0]?.content || input.contents?.[0]?.parts?.[0]?.text || ''
    const content = prompt.includes('糖果') ? candy : prompt === 'hi' ? identity : picture
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'X-Client-Request-ID': 'local-preview-' + sequence })
    res.flushHeaders()
    let offset = 0
    const gemini = path.includes(':streamGenerateContent')
    const send = () => {
      if (res.destroyed) return
      const delta = content.slice(offset, offset+16); offset += 16
      const final = offset >= content.length
      const payload = gemini ? { candidates: [{ content: { parts: [{ text: delta }] }, ...(final ? { finishReason: 'STOP' } : {}) }] } : { choices: [{ delta: { content: delta }, finish_reason: final ? 'stop' : null }] }
      res.write('data: ' + JSON.stringify(payload) + '\n\n')
      if (final) { if (!gemini) res.write('data: [DONE]\n\n'); res.end() }
      else timer = setTimeout(send, 90)
    }
    let timer = setTimeout(send, 1600)
    res.on('close', () => clearTimeout(timer)); return
  }
  json(res, {}, 404)
}
process.chdir(root)
const server = await createServer({
  define: { 'import.meta.env.VITE_API_BASE_URL': JSON.stringify('/api/v1'), 'import.meta.env.VITE_DEV_PROXY_TARGET': JSON.stringify('') },
  root, configFile: false, plugins: [{
    name: 'model-quality-local-preview', enforce: 'pre',
    resolveId(id) { if (virtual[id]) return '\0preview:' + virtual[id]; if (id.startsWith('preview:')) return '\0' + id; if (id === '/preview-entry.js') return '\0preview:entry' },
    load(id) { if (id.startsWith('\0preview:')) return modules[id.slice('\0preview:'.length)] },
    configureServer(instance) { instance.middlewares.use((req, res, next) => { handle(req, res, next).catch(() => json(res, {}, 400)) }) }
  }, vue()], resolve: { alias: [
    ...Object.entries(virtual).map(([find, name]) => ({ find, replacement: 'preview:' + name })),
    { find: '@', replacement: resolve(root, 'src') },
    { find: 'vue-i18n', replacement: resolve(root, 'node_modules/vue-i18n/dist/vue-i18n.runtime.esm-bundler.js') }
  ] },
  optimizeDeps: { entries: [], include: ['vue', 'vue-i18n', 'axios', 'marked', 'dompurify', 'katex'] },
  server: { host: '127.0.0.1', port, strictPort: true }
})
await server.listen()
process.stdout.write('本地模拟验收：http://127.0.0.1:' + port + '/model-quality\n不连接生产，不使用真实密钥。\n')
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, async () => { await server.close(); process.exit(0) })
