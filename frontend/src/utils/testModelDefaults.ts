function modelVersion(id: string): number[] {
  // 日期快照不是版本；Claude 的 4-5 与 4.5 按相同版本比较。
  const name = id.replace(/[-_]\d{4}-\d{2}-\d{2}(?=$|[-_])/g, '').replace(/[-_]\d{8}(?=$|[-_])/g, '')
  // 仅 Claude 用连字符表示次版本，其他平台的 -0613、-32k 等是快照或规格。
  const pattern = /claude/i.test(name) ? /\d+(?:[.-]\d+(?![\da-z]))*/i : /\d+(?:\.\d+)*/
  return (name.match(pattern)?.[0] || '').split(/[.-]/).filter(Boolean).map(Number)
}

export function compareTestModelIds(a: string, b: string): number {
  const left = modelVersion(a)
  const right = modelVersion(b)
  for (let index = 0; index < Math.max(left.length, right.length); index++) {
    const difference = (right[index] ?? 0) - (left[index] ?? 0)
    if (difference) return difference
  }
  // 同版本使用统一权重，混合平台列表也能稳定保证 GPT Sol、Astra 优先。
  const preference = (id: string) => /gpt/i.test(id) && /\bsol\b/i.test(id) ? 0 : /gpt/i.test(id) && /\bastra\b/i.test(id) ? 1 : 2
  return preference(a) - preference(b)
}
