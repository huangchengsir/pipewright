import { createPinia, setActivePinia } from 'pinia'
import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from '../../stores/session'
import { usePermissionStore } from '../../stores/permissions'
import RunTerminal from './RunTerminal.vue'

const api=vi.hoisted(()=>({logs:vi.fn(),subscribe:vi.fn(),copy:vi.fn()}))
vi.mock('../../api/runs',()=>({getRunLogs:api.logs,subscribeRunEvents:api.subscribe}))
const line=(seq:number,text:string,ordinal=0)=>({seq,text,stepOrdinal:ordinal,ts:'',stream:'stdout'})
let previousClipboard:PropertyDescriptor|undefined
beforeEach(()=>{
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'employee',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('team-a')
  previousClipboard=Object.getOwnPropertyDescriptor(navigator,'clipboard')
  Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:api.copy}})
  api.copy.mockResolvedValue(undefined)
  api.logs.mockResolvedValue({lines:[line(4,'first match'),line(12,'second match value=[MASKED]'),line(24,'other ordinal',1)],complete:true,nextSeq:25})
})
afterEach(()=>{
  if(previousClipboard)Object.defineProperty(navigator,'clipboard',previousClipboard)
  else Reflect.deleteProperty(navigator,'clipboard')
})

describe('current-run terminal native controls',()=>{
  it('clicks both exact search directions and selects the current masked range via native line buttons',async()=>{
    const w=mount(RunTerminal,{props:{runId:'run',live:false,filterOrdinal:0}})
    try {
      await flushPromises()
      await w.get('input[type=search]').setValue('match')
      await w.get('button[aria-label="下一个匹配"]').trigger('click')
      await flushPromises()
      expect(w.get('.term-line--active').attributes('data-log-seq')).toBe('12')
      await w.get('button[aria-label="上一个匹配"]').trigger('click')
      await flushPromises()
      expect(w.get('.term-line--active').attributes('data-log-seq')).toBe('4')
      await w.get('[data-log-seq="4"] button').trigger('click')
      await w.get('[data-log-seq="12"] button').trigger('click',{shiftKey:true})
      expect(w.findAll('.term-line--selected')).toHaveLength(2)
      await w.findAll('button').find(b=>b.text()==='复制脱敏片段')!.trigger('click')
      expect(api.copy).toHaveBeenCalledExactlyOnceWith('4: first match\n12: second match value=[MASKED]')
      expect(api.logs).toHaveBeenCalledExactlyOnceWith('run',0,expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'}}))
    } finally {w.unmount()}
  })

  it('clears manual copy, selected range and stale native actions after the old team is replaced',async()=>{
    api.copy.mockRejectedValue(new Error('denied'))
    const w=mount(RunTerminal,{props:{runId:'run',live:false,filterOrdinal:0}})
    try {
      await flushPromises()
      const lineButton=w.get('[data-log-seq="12"] button')
      await lineButton.trigger('click')
      const copy=w.findAll('button').find(b=>b.text()==='复制脱敏片段')!
      await copy.trigger('click')
      await flushPromises()
      expect(w.get<HTMLTextAreaElement>('textarea.term-manual').element.value).toContain('second match value=[MASKED]')
      usePermissionStore().setTeam('team-b')
      await flushPromises()
      expect(w.find('textarea.term-manual').exists()).toBe(false)
      expect(w.findAll('input[type=number]').map(el=>(el.element as HTMLInputElement).value)).toEqual(['',''])
      await lineButton.trigger('click')
      await copy.trigger('click')
      await flushPromises()
      expect(api.copy).toHaveBeenCalledOnce()
    } finally {w.unmount()}
  })

  it('pages forward and back in the current read-only log buffer without another request',async()=>{
    api.logs.mockResolvedValue({lines:Array.from({length:600},(_,i)=>line(i+1,'row '+i)),complete:true,nextSeq:601})
    const w=mount(RunTerminal,{props:{runId:'large',live:false}})
    try {
      await flushPromises()
      const [prev,next]=w.findAll('.term-pages button')
      expect(prev!.attributes('disabled')).toBeDefined()
      await next!.trigger('click')
      await flushPromises()
      expect(w.get('.term-line').attributes('data-log-seq')).toBe('501')
      await prev!.trigger('click')
      await flushPromises()
      expect(w.get('.term-line').attributes('data-log-seq')).toBe('1')
      expect(api.logs).toHaveBeenCalledOnce()
    } finally {w.unmount()}
  })
})
