import { describe, expect, it } from 'vitest'
import { renderMarkdown } from '../markdown'

describe('Markdown 回复渲染', () => {
  it('在 Markdown 转义之前识别 LaTeX，并渲染盒装答案与行内公式', () => {
    const html = renderMarkdown(String.raw`答案为 \(x=21\)。

\[
\boxed{21\text{颗}}.
\]`)
    expect(html).toContain('class="katex"')
    expect(html).toContain('katex-display')
    expect(html).not.toContain('<p>[<br>')
  })

  it('保留代码示例内的公式原文，不把普通价格当成公式', () => {
    const html = renderMarkdown('价格 $5，另一个 $10。\n\n`\\(x\\)`\n\n```latex\n\\[x\\]\n```')
    expect(html).not.toContain('class="katex"')
    expect(html).toContain('价格 $5')
    expect(html).toContain('\\[x\\]')
  })

  it('流式未闭合公式不会抛错，公式不能注入外部链接或 HTML', () => {
    expect(() => renderMarkdown(String.raw`\[\boxed{21`)).not.toThrow()
    const html = renderMarkdown(String.raw`\(\href{javascript:alert(1)}{test}\)`)
    expect(html).not.toContain('href="javascript:')
  })

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
