import DOMPurify from 'dompurify'
import { Marked, type Tokens } from 'marked'
import katex from 'katex'
import 'katex/dist/katex.min.css'

// 在 Markdown 的反斜杠转义之前识别公式；代码块仍交给原有 tokenizer。
const markdown = new Marked({ async: false, gfm: true, breaks: true }, {
  extensions: [
    {
      name: 'mathBlock', level: 'block',
      start: source => source.search(/\\\[|\$\$/),
      tokenizer(source) {
        const match = /^(?:\\\[([\s\S]+?)\\\]|\$\$([\s\S]+?)\$\$)(?:\n|$)/.exec(source)
        if (match) return { type: 'mathBlock', raw: match[0], text: match[1] ?? match[2], display: true }
      },
      renderer: token => renderMath(token)
    },
    {
      name: 'mathInline', level: 'inline',
      start: source => source.search(/\\\(|\$/),
      tokenizer(source) {
        const match = /^(?:\\\(([^\n]*?)\\\)|\$(?![\s$])([^\n$]*?[^\s$])\$(?!\d))/.exec(source)
        if (match) return { type: 'mathInline', raw: match[0], text: match[1] ?? match[2], display: false }
      },
      renderer: token => renderMath(token)
    }
  ]
})

function renderMath(token: Tokens.Generic): string {
  return katex.renderToString(token.text, {
    displayMode: token.display, throwOnError: false, trust: false, strict: 'ignore', maxExpand: 200
  })
}

export function renderMarkdown(content: string): string {
  const html = markdown.parse(content) as string
  return DOMPurify.sanitize(html)
}
