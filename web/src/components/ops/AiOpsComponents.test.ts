import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AiOpsServerPicker from './AiOpsServerPicker.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import AiOpsToolResult from './AiOpsToolResult.vue'
import AiOpsMessageList from './AiOpsMessageList.vue'
import AiOpsConfirmation from './AiOpsConfirmation.vue'
import AiOpsAnalysisConsent from './AiOpsAnalysisConsent.vue'
import AiOpsSessionList from './AiOpsSessionList.vue'
import type { Call, Confirmation, Preview } from '../../api/opsChat'
const call: Call = {
  callId: 'c',
  runId: 'r',
  sessionId: 'chat',
  serverId: 'a',
  serverName: 'Frozen server A',
  toolId: 'docker_action',
  args: { container: 'web', action: 'stop' },
  object: 'a'.repeat(64),
  status: 'awaiting_confirmation',
  argsHash: 'args',
  targetHash: 'target',
  output: '',
  error: '',
  truncated: false,
}
const confirmation: Confirmation = {
  runId: 'r',
  nonce: 'n',
  expiresAt: new Date(Date.now() + 60000).toISOString(),
  valid: true,
  calls: [
    {
      callId: 'c',
      serverId: 'a',
      toolId: 'docker_action',
      object: call.object,
      argsHash: 'args',
      targetHash: 'target',
    },
  ],
}
describe('ops components', () => {
  it('switching sessions clears a previous rename edit', async () => {
    const w = mount(AiOpsSessionList, {
      props: {
        sessions: [
          { id: 'a', title: 'A', updatedAt: '' },
          { id: 'b', title: 'B', updatedAt: '' },
        ] as never,
        currentId: 'a',
        disabled: false,
      },
    })
    await w.get('[class="ops-btn"]').trigger('click')
    await w.get('input').setValue('wrong title')
    await w.setProps({ currentId: 'b' })
    expect(w.find('form').exists()).toBe(false)
    expect(w.emitted('rename')).toBeUndefined()
    w.unmount()
  })
  it('server picker emits new selection, keeps missing saved targets and caps additions', async () => {
    const w = mount(AiOpsServerPicker, {
      props: {
        servers: [{ id: 'b', name: 'B', host: 'host', port: 22 }] as never,
        selected: ['gone'],
        disabled: false,
      },
    })
    expect(w.text()).toContain('服务器已删除')
    await w.findAll('input')[0]!.setValue(true)
    expect(w.emitted('change')).toEqual([[['gone', 'b']]])
    expect(w.props('selected')).toEqual(['gone'])
    w.unmount()
  })
  it('manual tools remain available without model; forms build only typed arguments', async () => {
    const w = mount(AiOpsComposer, {
      props: {
        draft: '',
        draftStatus: 'saved',
        tools: [{ toolId: 'host_resources', mutation: false, schema: {} }],
        disabled: false,
        active: false,
        modelAvailable: false,
        hasTargets: true,
        pending: false,
      },
    })
    await w.get('.tool-picker summary').trigger('click')
    const collect = w.findAll('button').find((b) => b.text() === '采集数据')!
    expect(collect.attributes('disabled')).toBeUndefined()
    await collect.trigger('click')
    expect(w.emitted('tool')).toEqual([['host_resources', {}]])
    expect(w.get('[aria-label="发送"]').attributes('disabled')).toBeDefined()
    expect(w.find('select').exists()).toBe(false)
    w.unmount()
  })
  it('confirms the exact full batch and never guesses missing actions', async () => {
    const w = mount(AiOpsConfirmation, {
      props: { confirmation, calls: {}, disabled: false },
    })
    expect(w.text()).not.toContain('重启')
    expect(w.text()).toContain('正在读取历史结果')
    expect(w.get('.ops-primary').attributes('disabled')).toBeDefined()
    await w.setProps({ calls: { c: call } })
    await w.get('.ops-primary').trigger('click')
    expect(w.emitted('confirm')).toEqual([[confirmation]])
    expect(w.text()).toContain('停止')
    w.unmount()
  })
  it('expired batch only offers reissue; no approval auto-sent', async () => {
    const w = mount(AiOpsConfirmation, {
      props: {
        confirmation: { ...confirmation, valid: false },
        calls: { c: call },
        disabled: false,
      },
    })
    expect(w.find('.ops-primary').exists()).toBe(false)
    await w.findAll('button')[0]!.trigger('click')
    expect(w.emitted('reissue')).toEqual([['r']])
    expect(w.emitted('confirm')).toBeUndefined()
    w.unmount()
  })
  it.each(['unknown', 'interrupted'] as const)(
    'does not expose blind retry for %s',
    (status) => {
      const w = mount(AiOpsToolResult, {
        props: {
          call: { ...call, status },
          disabled: false,
          modelAvailable: false,
        },
      })
      expect(w.find('[aria-label="仅重试此失败调用"]').exists()).toBe(false)
      expect(w.text()).toContain('远端影响尚未核实')
      expect(w.text()).toContain('Frozen server A')
      w.unmount()
    },
  )
  it('renders partial structured metrics and retries exactly the failed result', async () => {
    const w = mount(AiOpsToolResult, {
      props: {
        call: {
          ...call,
          status: 'partial_failed',
          truncated: true,
          exitCode: 1,
          resources: {
            disks: null,
            memory: {
              totalBytes: 1073741824,
              usedBytes: 536870912,
              availableBytes: 536870912,
              swapTotalBytes: 0,
              swapUsedBytes: 0,
            },
            load: null,
            unavailable: ['load', 'disks'],
          },
        },
        disabled: false,
        modelAvailable: false,
      },
    })
    expect(w.text()).toContain('512.0 MiB / 1.0 GiB')
    expect(w.text()).toContain('未取得指标')
    await w.get('[aria-label="复制"]').trigger('click')
    const copied = JSON.parse(w.emitted('copy')![0]![0] as string)
    expect(copied.serverId).toBe('a')
    expect(copied.resources.memory.usedBytes).toBe(536870912)
    await w.get('[aria-label="仅重试此失败调用"]').trigger('click')
    expect(w.emitted('retry')).toEqual([['c']])
    w.unmount()
  })
  it('assistant manual advice is copy-only and seq updates do not duplicate cards', async () => {
    const entries = [
      {
        sessionId: 'chat',
        seq: 1,
        kind: 'assistant',
        text: 'rm -rf /example',
        createdAt: '',
      },
      {
        sessionId: 'chat',
        seq: 2,
        kind: 'call_status',
        callId: 'c',
        createdAt: '',
      },
      {
        sessionId: 'chat',
        seq: 3,
        kind: 'call_status',
        callId: 'c',
        createdAt: '',
      },
    ]
    const w = mount(AiOpsMessageList, {
      props: {
        entries,
        calls: { c: call },
        disabled: false,
        modelAvailable: false,
        hasHistory: false,
      },
    })
    expect(w.findAllComponents(AiOpsToolResult)).toHaveLength(1)
    await w
      .findAll('button')
      .find((b) => b.text() === '复制')!
      .trigger('click')
    expect(w.emitted('copy')).toEqual([['rm -rf /example']])
    expect(w.emitted('execute')).toBeUndefined()
    w.unmount()
  })
  it('analysis shows actual payload and destination; new previews reset checkbox', async () => {
    const preview: Preview = {
      callIds: ['c'],
      items: [
        {
          callId: 'c',
          targetAlias: 'target_0',
          toolId: 'docker_logs',
          output: 'actual output',
          error: 'actual error',
          status: 'failed',
        },
      ],
      hash: 'hash',
      provider: {
        provider: 'configured-provider',
        model: 'model',
        configHash: 'config-hash',
      },
    }
    const w = mount(AiOpsAnalysisConsent, {
      props: { preview, calls: { c: call }, disabled: false },
    })
    expect(w.text()).toContain('actual output')
    expect(w.text()).toContain('actual error')
    expect(w.text()).toContain('configured-provider')
    expect(w.get('.ops-primary').attributes('disabled')).toBeDefined()
    await w.get('input').setValue(true)
    await w.get('.ops-primary').trigger('click')
    expect(w.emitted('approve')).toHaveLength(1)
    await w.setProps({ preview: { ...preview, hash: 'new' } })
    expect((w.get('input').element as HTMLInputElement).checked).toBe(false)
    w.unmount()
  })
})
