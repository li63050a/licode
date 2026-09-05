function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function renderCodeBlock(c: { lang: string; code: string }): string {
  const enc = encodeURIComponent(c.code)
  return `<div class="md-pre"><div class="md-pre-head"><span>${escapeHtml(c.lang) || '代码'}</span><button class="md-copy" data-code="${enc}" type="button">复制</button></div><pre><code>${escapeHtml(c.code)}</code></pre></div>`
}

export function renderMarkdown(src: string): string {
  if (!src) return ''
  const codes: { lang: string; code: string }[] = []
  let text = src.replace(/```(\w*)[ \t]*\n?([\s\S]*?)```/g, (_m, lang: string, code: string) => {
    codes.push({ lang: lang || '', code: code.replace(/\n$/, '') })
    return `\u0000CODE${codes.length - 1}\u0000`
  })
  text = escapeHtml(text)
  text = text
    .replace(/`([^`\n]+)`/g, '<code class="md-code">$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>')
    .replace(
      /\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g,
      '<a href="$2" target="_blank" rel="noopener" class="md-link">$1</a>',
    )

  const lines = text.split('\n')
  const out: string[] = []
  let listType: 'ul' | 'ol' | null = null
  const closeList = () => {
    if (listType) {
      out.push(`</${listType}>`)
      listType = null
    }
  }
  for (const line of lines) {
    const codeMatch = line.match(/^\u0000CODE(\d+)\u0000\s*$/)
    if (codeMatch) {
      closeList()
      out.push(renderCodeBlock(codes[Number(codeMatch[1])]))
      continue
    }
    const h = line.match(/^(#{1,6})\s+(.*)$/)
    if (h) {
      closeList()
      const n = Math.min(h[1].length + 1, 5)
      out.push(`<h${n} class="md-h md-h${n}">${h[2]}</h${n}>`)
      continue
    }
    const quote = line.match(/^&gt;\s?(.*)$/)
    if (quote) {
      closeList()
      out.push(`<blockquote class="md-quote">${quote[1]}</blockquote>`)
      continue
    }
    const ul = line.match(/^\s*[-*]\s+(.*)$/)
    const ol = line.match(/^\s*\d+[.、]\s+(.*)$/)
    if (ul) {
      if (listType !== 'ul') {
        closeList()
        out.push('<ul class="md-list">')
        listType = 'ul'
      }
      out.push(`<li>${ul[1]}</li>`)
      continue
    }
    if (ol) {
      if (listType !== 'ol') {
        closeList()
        out.push('<ol class="md-list md-ol">')
        listType = 'ol'
      }
      out.push(`<li>${ol[1]}</li>`)
      continue
    }
    closeList()
    if (line.trim() === '') continue
    out.push(`<p class="md-p">${line}</p>`)
  }
  closeList()
  return out.join('\n')
}

export function prettyArgs(args: string): string {
  try {
    return JSON.stringify(JSON.parse(args), null, 2)
  } catch {
    return args
  }
}
