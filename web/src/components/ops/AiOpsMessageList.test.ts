import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AiOpsMessageList from './AiOpsMessageList.vue'
import type { Entry } from '../../api/opsChat'

describe('analysis failure messages', () => {
  it.each(['unknown','token_limit','timeout','rejected','invalid_response','unavailable','input_limit','invalid_plan'])('accurately renders and copies continuation %s without denying completed discovery', async (code) => {
    const w=mount(AiOpsMessageList,{props:{entries:[{sessionId:'s',seq:1,kind:'assistant',status:'failed',text:`[continuation:${code}]`,createdAt:''}],calls:{},disabled:false,modelAvailable:true,hasHistory:false}})
    expect(w.text()).toContain('已采集的事实已保留')
    expect(w.text()).toContain('未执行后续服务器任务')
    expect(w.text()).not.toContain('未执行服务器任务')
    expect(w.text()).not.toContain('[continuation:')
    await w.get('button[title="复制"]').trigger('click')
    expect(w.emitted('copy')?.[0]?.[0]).toContain('未执行后续服务器任务')
    w.unmount()
  })
  it('explains an invalid plan without displaying an internal marker', async () => {
    const w = mount(AiOpsMessageList, {
      props: {
        entries: [
          {
            sessionId: 's',
            seq: 1,
            kind: 'assistant',
            status: 'failed',
            text: '[planning:invalid_plan]',
            createdAt: '',
          },
          {
            sessionId: 's',
            seq: 2,
            kind: 'assistant',
            status: 'failed',
            text: 'Planning could not be completed.',
            createdAt: '',
          },
        ],
        calls: {},
        disabled: false,
        modelAvailable: true,
        hasHistory: false,
      },
    })
    expect(w.text()).toContain('任务或参数不受支持')
    expect(w.text()).toContain('旧请求未记录具体原因')
    expect(w.text()).not.toContain('[planning:')
    await w.findAll('button[title="复制"]')[0]!.trigger('click')
    expect(w.emitted('copy')?.[0]?.[0]).toContain('未执行服务器任务')
    w.unmount()
  })
  it.each([
    ['token_limit', '输出额度'],
    ['timeout', '超时'],
    ['rejected', '拒绝'],
    ['invalid_response', '无法解析'],
    ['unavailable', '不可用'],
    ['input_limit', '输入上限'],
  ])('renders and copies a localized %s error', async (code, text) => {
    const entry: Entry = {
      sessionId: 's',
      seq: 1,
      kind: 'analysis',
      status: 'failed',
      text: `[analysis:${code}]`,
      createdAt: '',
    }
    const w = mount(AiOpsMessageList, {
      props: {
        entries: [entry],
        calls: {},
        disabled: false,
        modelAvailable: true,
        hasHistory: false,
      },
    })
    expect(w.text()).toContain(text)
    expect(w.text()).not.toContain('[analysis:')
    await w.get('button[title="复制"]').trigger('click')
    expect(w.emitted('copy')?.[0]?.[0]).toContain(text)
    w.unmount()
  })
  it('localizes old generic failures without rewriting model answers', () => {
    const w = mount(AiOpsMessageList, {
      props: {
        entries: [
          {
            sessionId: 's',
            seq: 1,
            kind: 'analysis',
            status: 'failed',
            text: 'Analysis could not be completed; local tool results are preserved.',
            createdAt: '',
          },
          {
            sessionId: 's',
            seq: 2,
            kind: 'analysis',
            status: 'succeeded',
            text: '[analysis:timeout]',
            createdAt: '',
          },
        ],
        calls: {},
        disabled: false,
        modelAvailable: true,
        hasHistory: false,
      },
    })
    expect(w.text()).toContain('旧请求未记录具体原因')
    expect(w.text()).toContain('[analysis:timeout]')
    w.unmount()
  })
})
