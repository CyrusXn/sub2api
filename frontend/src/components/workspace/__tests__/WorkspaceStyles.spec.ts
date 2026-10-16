import { readFileSync } from 'node:fs'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { describe, expect, it } from 'vitest'

describe('工作台深色样式', () => {
  it.each([
    ['../WorkspaceImageStudio.vue', '.studio-connection'],
    ['../WorkspaceConnection.vue', '.connection-fields'],
    ['../../../views/user/WorkspaceView.vue', '.workspace-page']
  ])('%s 深色规则保留目标选择器，不能退化为仅修改根节点', (file, selector) => {
    const source = readFileSync(new URL(file, import.meta.url), 'utf8')
    const { descriptor } = parse(source)
    const css = compileStyle({ source: descriptor.styles[0].content, filename: file, id: 'data-v-test', scoped: true }).code
    expect(css).toContain(`.dark ${selector}`)
  })
})
