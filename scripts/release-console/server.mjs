import { createServer } from 'node:http'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { assertLoopbackHost, isAllowedAction } from './lib/policy.mjs'

const root = dirname(fileURLToPath(import.meta.url))
const host = '127.0.0.1'
const port = 3100
assertLoopbackHost(host)

const page = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sub2API 发布控制台</title><style>body{margin:0;background:#f6f8fb;color:#18212f;font:14px system-ui}.shell{max-width:1000px;margin:42px auto;padding:0 24px}.top{display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #dce2ea;padding-bottom:20px}h1{font-size:22px;margin:0}.badge{color:#087443;background:#e6f6ed;padding:5px 9px;border-radius:4px}section{margin-top:24px;border:1px solid #dce2ea;background:#fff;border-radius:7px;padding:20px}h2{font-size:16px;margin:0 0 12px}button{border:1px solid #c9d2dd;border-radius:5px;padding:9px 12px;background:#fff;color:#18212f;margin:0 8px 8px 0;cursor:pointer}button.primary{background:#1267c7;border-color:#1267c7;color:#fff}button.warn{color:#a32020;border-color:#e6b7b7}pre{min-height:220px;padding:14px;background:#101820;color:#cbe6d4;overflow:auto;white-space:pre-wrap;border-radius:5px}.note{color:#687486;line-height:1.5}</style><main class="shell"><div class="top"><div><h1>Sub2API 本机发布控制台</h1><p class="note">仅监听 127.0.0.1，生产操作使用固定白名单。</p></div><span class="badge">本机受限入口</span></div><section><h2>候选与蓝绿</h2><button class="primary" data-action="status">刷新状态</button><button class="primary" data-action="deploy">构建并一键无感部署</button><button data-action="build">仅构建候选</button><button data-action="stage-candidate">上传候选</button><button data-action="prepare-bluegreen">预热候选</button><button data-action="canary-green">切至候选验收</button><button data-action="return-blue">返回稳定蓝色</button><button class="warn" data-action="rollback">一键回滚蓝色</button><p class="note">一键部署会先创建数据库与配置快照、回滚镜像标签，再构建、校验、上传、预热候选并健康切流。任一步失败会停止，蓝色保持可用。</p></section><section><h2>执行日志</h2><pre id="out">等待操作...</pre></section></main><script>const out=document.querySelector('#out');document.querySelectorAll('button').forEach(b=>b.onclick=async()=>{out.textContent='执行 '+b.dataset.action+'...\\n';const r=await fetch('/api/'+b.dataset.action,{method:'POST'});out.textContent+=await r.text()})</script></html>`

function run(action) {
  return new Promise((resolve) => {
    const child = spawn(join(root, 'release-console.sh'), [action], { cwd: join(root, '../..') })
    let output = ''
    child.stdout.on('data', (chunk) => { output += chunk })
    child.stderr.on('data', (chunk) => { output += chunk })
    child.on('error', (error) => resolve({ code: 1, output: `${error.message}\n` }))
    child.on('close', (code) => resolve({ code, output }))
  })
}

let activeAction = null

createServer(async (request, response) => {
  if (request.method === 'GET' && request.url === '/') {
    const retentionPage = page
      .replace('一键回滚蓝色</button>', '一键回滚蓝色</button><button data-action="cleanup-release">清理旧发布资产</button>')
      .replace('任一步失败会停止，蓝色保持可用。', '任一步失败会停止，蓝色保持可用；健康切流后只保留当前版本和最近一次完整回滚资产。')
    return response.end(retentionPage)
  }
  const action = request.url?.match(/^\/api\/([a-z-]+)$/)?.[1]
  if (request.method !== 'POST' || !isAllowedAction(action)) { response.writeHead(404); return response.end('not found') }
  if (activeAction) { response.writeHead(409); return response.end(`已有操作执行中: ${activeAction}`) }
  activeAction = action
  let result
  try {
    result = await run(action)
  } finally {
    activeAction = null
  }
  response.writeHead(result.code === 0 ? 200 : 400, { 'content-type': 'text/plain; charset=utf-8' })
  response.end(result.output)
}).listen(port, host, () => console.log(`发布控制台: http://${host}:${port}`))
