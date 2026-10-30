// Search indices always refer to the original backend-redacted string.
export const MAX_LOG_HITS = 2000
export const MAX_LOG_QUERY_LENGTH = 200
export interface LogSearchHit { seq: number; index: number; start: number; end: number; hitIndex: number }
export function findLogMatches(lines: ReadonlyArray<{ seq: number; text: string }>, query: string) {
  const hits: LogSearchHit[] = []
  if (query.length > MAX_LOG_QUERY_LENGTH) return { hits, truncated: false, queryTooLong: true }
  if (!query) return { hits, truncated: false, queryTooLong: false }
  const expression = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'giu')
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]!
    expression.lastIndex = 0
    let match: RegExpExecArray | null
    while ((match = expression.exec(line.text)) !== null) {
      if (hits.length >= MAX_LOG_HITS) return { hits, truncated: true, queryTooLong: false }
      hits.push({ seq: line.seq, index, start: match.index, end: match.index + match[0].length, hitIndex: hits.length })
    }
  }
  return { hits, truncated: false, queryTooLong: false }
}
export interface LogTextPart { text: string; masked: boolean }
export interface LogHighlightPiece { parts: LogTextPart[]; hitIndex?: number }
/** One mark per retained hit, including hits which cross [MASKED] boundaries. */
export function highlightLogLine(text: string, hits: readonly LogSearchHit[]): LogHighlightPiece[] {
  const masks: { start: number; end: number }[] = []
  let found = text.indexOf('[MASKED]')
  while (found >= 0) { masks.push({ start: found, end: found + 8 }); found = text.indexOf('[MASKED]', found + 8) }
  let maskIndex = 0
  function parts(start: number, end: number): LogTextPart[] {
    const result: LogTextPart[] = []
    let at = start
    while (at < end) {
      const mask = masks[maskIndex]
      if (!mask || mask.start >= end) { result.push({ text: text.slice(at, end), masked: false }); break }
      if (mask.end <= at) { maskIndex++; continue }
      if (at < mask.start) { result.push({ text: text.slice(at, mask.start), masked: false }); at = mask.start }
      const to = Math.min(end, mask.end)
      result.push({ text: text.slice(at, to), masked: true }); at = to
      if (at >= mask.end) maskIndex++
    }
    return result
  }
  const result: LogHighlightPiece[] = []
  let cursor = 0
  for (const hit of hits) {
    if (hit.start > cursor) result.push({ parts: parts(cursor, hit.start) })
    result.push({ parts: parts(hit.start, hit.end), hitIndex: hit.hitIndex })
    cursor = hit.end
  }
  if (cursor < text.length) result.push({ parts: parts(cursor, text.length) })
  return result
}
