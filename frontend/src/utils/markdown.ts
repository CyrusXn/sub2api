import DOMPurify from 'dompurify'
import { marked } from 'marked'

export function renderMarkdown(content: string): string {
  const html = marked.parse(content, { async: false, gfm: true, breaks: true }) as string
  return DOMPurify.sanitize(html)
}
