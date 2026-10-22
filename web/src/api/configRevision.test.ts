import { describe, expect, it } from 'vitest'
import { reactive } from 'vue'
import { bindConfigRevision, configRevisionOf, configWriteOptions } from './configRevision'

describe('configuration revision bindings', () => {
  it('keeps each read DTO baseline separate and invisible to payload serialization', () => {
    const oldForm = reactive(bindConfigRevision({ name: 'before' }, 'cfg-1'))
    const laterRead = bindConfigRevision({ name: 'after' }, 'cfg-2')
    expect(configRevisionOf(oldForm)).toBe('cfg-1')
    expect(configRevisionOf(laterRead)).toBe('cfg-2')
    expect(JSON.stringify(oldForm)).toBe('{"name":"before"}')
    expect(configWriteOptions(configRevisionOf(oldForm))).toEqual({ headers: { 'If-Match': 'cfg-1' } })
  })
  it('advances only an explicitly selected snapshot and rejects malformed metadata', () => {
    const snapshot = bindConfigRevision({ stages: [] }, 'cfg-1')
    bindConfigRevision(snapshot, 'cfg-2')
    expect(configRevisionOf(snapshot)).toBe('cfg-2')
    expect(configRevisionOf(bindConfigRevision({}, 'untrusted\r\nheader'))).toBeUndefined()
    expect(configRevisionOf(undefined)).toBeUndefined()
    expect(configWriteOptions()).toBeUndefined()
  })
})
