<script setup lang="ts">
import { computed, h, type VNodeChild } from 'vue'
import { marked, type Token, type Tokens } from 'marked'

const props = defineProps<{
  text: string
  targetNames?: Record<string, string>
}>()
function targetName(alias: string): string {
  return /^target_(?:[A-H]|[0-7])$/.test(alias) &&
    props.targetNames &&
    Object.prototype.hasOwnProperty.call(props.targetNames, alias)
    ? props.targetNames![alias]
    : alias
}
function displayText(text: string): string {
  return text.replace(
    /(^|[^\p{L}\p{N}_]|\p{Script=Han})(target_(?:[A-H]|[0-7]))(?=\p{Script=Han}|[^\p{L}\p{N}_]|$)/gu,
    (_match, prefix: string, alias: string) => prefix + targetName(alias),
  )
}
function hasExcessiveQuoteDepth(text: string): boolean {
  for (const line of text.split('\n')) {
    let offset = 0
    let depth = 0
    while (offset < line.length) {
      let spaces = 0
      while (spaces < 3 && line[offset] === ' ') {
        spaces++
        offset++
      }
      if (line[offset] !== '>') break
      offset++
      if (++depth >= 65) return true
      if (line[offset] === ' ' || line[offset] === '\t') offset++
    }
  }
  return false
}
const tokens = computed(() => {
  if (hasExcessiveQuoteDepth(props.text)) return null
  try {
    return marked.lexer(props.text, { gfm: true, breaks: true })
  } catch {
    return null
  }
})

function plain(text: string): string {
  const decoder = document.createElement('textarea')
  decoder.innerHTML = text.replace(/</g, '&lt;').replace(/>/g, '&gt;')
  return decoder.value
}
function safeLink(href: string): string | undefined {
  try {
    const url = new URL(href)
    if (
      ['http:', 'https:'].includes(url.protocol) &&
      !url.username &&
      !url.password
    )
      return url.href
  } catch {
    /* Relative and non-web links are rendered as text. */
  }
  return undefined
}
// Marked handles parsing. Vue text nodes handle escaping; no raw HTML is inserted.
function renderItems(items: Token[] = [], depth = 0): VNodeChild[] {
  if (depth >= 64) return items.map((token) => token.raw)
  const renderTokens = (children: Token[] = []) =>
    renderItems(children, depth + 1)
  return items.map((token): VNodeChild => {
    const children = 'tokens' in token ? token.tokens : undefined
    switch (token.type) {
      case 'space':
        return null
      case 'html':
        return token.raw
      case 'heading':
        return h(
          'h' + Math.min(6, Math.max(1, token.depth)),
          renderTokens(token.tokens),
        )
      case 'paragraph':
        return h('p', renderTokens(token.tokens))
      case 'text':
        return children
          ? renderTokens(children)
          : displayText(plain(token.text))
      case 'escape':
        return displayText(plain(token.text))
      case 'strong':
        return h('strong', renderTokens(token.tokens))
      case 'em':
        return h('em', renderTokens(token.tokens))
      case 'del':
        return h('del', renderTokens(token.tokens))
      case 'br':
        return h('br')
      case 'hr':
        return h('hr')
      case 'codespan':
        return h('code', targetName(plain(token.text)))
      case 'code':
        return h('pre', [h('code', token.text)])
      case 'blockquote':
        return h('blockquote', renderTokens(token.tokens))
      case 'image':
        return plain(token.text)
      case 'link': {
        const href = safeLink(plain(token.href))
        return href
          ? h(
              'a',
              { href, target: '_blank', rel: 'noopener noreferrer' },
              renderTokens(token.tokens),
            )
          : renderTokens(token.tokens)
      }
      case 'list':
        return h(
          token.ordered ? 'ol' : 'ul',
          token.ordered ? { start: token.start } : {},
          token.items.map((item: Tokens.ListItem) =>
            h('li', [
              item.task
                ? h('input', {
                    type: 'checkbox',
                    disabled: true,
                    checked: item.checked,
                  })
                : null,
              ...renderTokens(item.tokens),
            ]),
          ),
        )
      case 'table':
        return h('div', { class: 'markdown-table', tabindex: 0 }, [
          h('table', [
            h('thead', [
              h(
                'tr',
                token.header.map((cell: Tokens.TableCell) =>
                  h('th', renderTokens(cell.tokens)),
                ),
              ),
            ]),
            h(
              'tbody',
              token.rows.map((row: Tokens.TableCell[]) =>
                h(
                  'tr',
                  row.map((cell) => h('td', renderTokens(cell.tokens))),
                ),
              ),
            ),
          ]),
        ])
      default:
        return token.raw
    }
  })
}
const render = () => (tokens.value ? renderItems(tokens.value) : props.text)
</script>

<template>
  <div class="ops-markdown"><component :is="render" /></div>
</template>

<style scoped>
.ops-markdown {
  min-width: 0;
  overflow-wrap: anywhere;
  line-height: 1.65;
}
.ops-markdown :deep(p) {
  margin: 0 0 10px;
}
.ops-markdown :deep(h1),
.ops-markdown :deep(h2) {
  font-size: 16px;
}
.ops-markdown :deep(h3),
.ops-markdown :deep(h4),
.ops-markdown :deep(h5),
.ops-markdown :deep(h6) {
  font-size: 14px;
}
.ops-markdown :deep(h1),
.ops-markdown :deep(h2),
.ops-markdown :deep(h3),
.ops-markdown :deep(h4),
.ops-markdown :deep(h5),
.ops-markdown :deep(h6) {
  margin: 16px 0 8px;
  line-height: 1.4;
}
.ops-markdown :deep(ul),
.ops-markdown :deep(ol) {
  margin: 8px 0;
  padding-left: 22px;
}
.ops-markdown :deep(li > p) {
  margin-bottom: 4px;
}
.ops-markdown :deep(code) {
  font-family: var(--font-mono);
  font-size: 12px;
  background: var(--color-inset);
  padding: 2px 4px;
  border-radius: 3px;
}
.ops-markdown :deep(pre) {
  margin: 10px 0;
  padding: 12px;
  max-height: 320px;
  overflow: auto;
  background: var(--color-inset);
  border-radius: 4px;
  white-space: pre;
}
.ops-markdown :deep(pre code) {
  padding: 0;
}
.ops-markdown :deep(blockquote) {
  margin: 10px 0;
  padding-left: 12px;
  border-left: 2px solid var(--color-border-strong);
  color: var(--color-dim);
}
.ops-markdown :deep(a) {
  color: var(--color-primary);
  text-decoration: underline;
}
.ops-markdown :deep(.markdown-table) {
  max-width: 100%;
  overflow: auto;
  margin: 10px 0;
}
.ops-markdown :deep(table) {
  border-collapse: collapse;
  width: 100%;
}
.ops-markdown :deep(th),
.ops-markdown :deep(td) {
  border: 1px solid var(--color-border);
  padding: 6px 8px;
  text-align: left;
}
.ops-markdown :deep(th) {
  background: var(--color-inset);
}
.ops-markdown :deep(hr) {
  border: 0;
  border-top: 1px solid var(--color-border);
  margin: 12px 0;
}
.ops-markdown :deep(:first-child) {
  margin-top: 0;
}
.ops-markdown :deep(:last-child) {
  margin-bottom: 0;
}
</style>
