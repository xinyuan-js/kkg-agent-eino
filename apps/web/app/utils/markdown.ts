export function renderMarkdown(markdown: string) {
  const lines = normalizeMarkdownFences(markdown).split('\n')
  const html: string[] = []
  let paragraph: string[] = []
  let listOpen = false
  let codeOpen = false
  let codeLines: string[] = []

  const closeParagraph = () => {
    if (!paragraph.length) {
      return
    }
    html.push(`<p>${paragraph.map(renderInline).join('<br>')}</p>`)
    paragraph = []
  }

  const closeList = () => {
    if (!listOpen) {
      return
    }
    html.push('</ul>')
    listOpen = false
  }

  const closeCode = () => {
    html.push(`<pre><code>${escapeHtml(codeLines.join('\n'))}</code></pre>`)
    codeLines = []
    codeOpen = false
  }

  for (const rawLine of lines) {
    const line = rawLine.trimEnd()
    const trimmed = line.trim()

    if (trimmed.startsWith('```')) {
      if (codeOpen) {
        closeCode()
      } else {
        closeParagraph()
        closeList()
        codeOpen = true
        codeLines = []
      }
      continue
    }

    if (codeOpen) {
      codeLines.push(line)
      continue
    }

    if (!trimmed) {
      closeParagraph()
      closeList()
      continue
    }

    const heading = trimmed.match(/^(#{1,4})\s+(.+)$/)
    if (heading) {
      closeParagraph()
      closeList()
      const level = (heading[1] || '').length
      html.push(`<h${level}>${renderInline(heading[2] || '')}</h${level}>`)
      continue
    }

    const listItem = trimmed.match(/^[-*]\s+(.+)$/)
    if (listItem) {
      closeParagraph()
      if (!listOpen) {
        html.push('<ul>')
        listOpen = true
      }
      html.push(`<li>${renderInline(listItem[1] || '')}</li>`)
      continue
    }

    paragraph.push(trimmed)
  }

  if (codeOpen) {
    closeCode()
  }
  closeParagraph()
  closeList()
  return html.join('')
}

function normalizeMarkdownFences(markdown: string) {
  return normalizeCodeMarkers(markdown)
}

function normalizeCodeMarkers(markdown: string) {
  const output: string[] = []
  const lines = markdown.replace(/\r\n/g, '\n').split('\n')

  for (const rawLine of lines) {
    const trimmed = rawLine.trim()
    const open = trimmed.match(/^<kkg-code(?:\s+lang=["']?([A-Za-z0-9_+.-]+)["']?)?\s*>$/i)
    if (open) {
      output.push(open[1] ? `\`\`\`${open[1]}` : '```')
      continue
    }
    if (/^<\/kkg-code>$/i.test(trimmed)) {
      output.push('```')
      continue
    }
    output.push(rawLine)
  }

  return output.join('\n')
}

function renderInline(value: string) {
  return escapeHtml(value)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\[([^\]]+)]\(([^)\s]+)\)/g, (_match, label, href) => {
      const safe = safeHref(String(href))
      if (!safe) {
        return label
      }
      return `<a href="${safe}" target="_blank" rel="noreferrer">${label}</a>`
    })
}

function safeHref(value: string) {
  const href = value.replaceAll('&amp;', '&').trim()
  if (href.startsWith('/') || href.startsWith('http://') || href.startsWith('https://')) {
    return escapeHtml(href)
  }
  return ''
}

function escapeHtml(value: string) {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}
