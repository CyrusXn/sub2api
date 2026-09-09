import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const scriptPath = fileURLToPath(new URL('../remote-bluegreen.sh', import.meta.url))
const script = readFileSync(scriptPath, 'utf8')

test('蓝绿流量槽位保持 api_only', () => {
  assert.match(
    script,
    /run_api_slot\(\)[\s\S]*?-e DEPLOYMENT_ROLE=api_only/,
  )
})

test('缺失时可恢复唯一 primary 后台节点', () => {
  assert.match(script, /^PRIMARY_NAME=sub2api$/m)
  assert.match(
    script,
    /ensure_primary_slot\(\)[\s\S]*?docker compose up -d --no-deps --force-recreate --pull never sub2api/,
  )
  assert.match(script, /^  ensure-primary\)$/m)
})

test('发布资产清理始终保留 primary 后台节点', () => {
  assert.match(
    script,
    /for slot in[\s\S]*?\[\[ "\$slot" == "\$PRIMARY_NAME" \]\] && continue/,
  )
})
