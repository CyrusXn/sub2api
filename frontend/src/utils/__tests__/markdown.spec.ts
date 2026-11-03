import { describe, expect, it } from 'vitest'
import { renderMarkdown } from '../markdown'

describe('Markdown 回复渲染', () => {
  it('解析 GFM 格式并过滤可执行标签', () => {
    const html = renderMarkdown('**重点**\n\n- 第一项\n- 第二项\n\n| 名称 | 数量 |\n| --- | ---: |\n| 糖果 | 21 |\n\n<script>alert(1)</script>')
    const content = document.createElement('div')
    content.innerHTML = html
    expect(content.querySelector('strong')?.textContent).toBe('重点')
    expect(content.querySelectorAll('ul li')).toHaveLength(2)
    expect(content.querySelector('table td')?.textContent).toBe('糖果')
    expect(content.querySelector('script')).toBeNull()
  })
})
