import { describe, expect, it } from 'vitest'
import { findLogMatches, highlightLogLine, MAX_LOG_HITS } from './logSearch'
describe('bounded original-string log search', () => {
 it('treats regular-expression syntax literally and preserves Unicode offsets', () => {
  expect(findLogMatches([{ seq: 1, text: 'İfoo [x].* token=[MASKED]' }], '[x].*').hits[0]).toMatchObject({ start: 5, end: 10 })
  const result = findLogMatches([{ seq: 7, text: 'İfoo' }], 'foo')
  expect(result.hits[0]).toMatchObject({ seq: 7, start: 1, end: 4 })
  const pieces = highlightLogLine('İfoo', result.hits); expect(pieces.find(p => p.hitIndex === 0)?.parts.map(p => p.text).join('')).toBe('foo')
 })
 it('bounds a million-character single line and the rendered mark count', () => {
  const text = 'a'.repeat(1000000); const result = findLogMatches([{ seq: 9, text }], 'a')
  expect(result.hits).toHaveLength(MAX_LOG_HITS); expect(result.truncated).toBe(true)
  const pieces = highlightLogLine(text, result.hits); expect(pieces.filter(p => p.hitIndex != null)).toHaveLength(MAX_LOG_HITS)
  expect(pieces.flatMap(p => p.parts).map(p => p.text).join('')).toBe(text)
 })
 it('keeps masked styling through a match crossing redaction-token boundaries', () => {
  const text = 'token=[MASKED]'; const hits = findLogMatches([{ seq: 1, text }], 'n=[MASK').hits
  const parts = highlightLogLine(text, hits).flatMap(p => p.parts)
  expect(parts.filter(p => p.masked).map(p => p.text).join('')).toBe('[MASKED]')
  expect(parts.map(p => p.text).join('')).toBe(text)
 })
 it('rejects an overlong pattern without compiling a large regular expression', () => {
  const result = findLogMatches([{ seq: 1, text: 'a'.repeat(1000) }], 'a'.repeat(201))
  expect(result.queryTooLong).toBe(true); expect(result.hits).toHaveLength(0); expect(result.truncated).toBe(false)
 })

})
