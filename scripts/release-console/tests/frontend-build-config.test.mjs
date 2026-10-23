import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../../..', import.meta.url))

test('生产构建不重复启动 vue-tsc 检查器', async () => {
  const source = await readFile(`${root}/frontend/vite.config.ts`, 'utf8')
  const checkerConfig = source.match(/checker\(\{[\s\S]*?\}\)/)?.[0]

  assert.ok(checkerConfig, '缺少 vite-plugin-checker 配置')
  assert.match(checkerConfig, /vueTsc:\s*true/)
  assert.match(checkerConfig, /enableBuild:\s*false/)
})
