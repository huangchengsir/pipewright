import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AiOpsServerPicker from './AiOpsServerPicker.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import AiOpsExecArgs from './AiOpsExecArgs.vue'
import AppSelect from '../ui/AppSelect.vue'
import AiOpsToolResult from './AiOpsToolResult.vue'
import AiOpsMessageList from './AiOpsMessageList.vue'
import AiOpsConfirmation from './AiOpsConfirmation.vue'
import AiOpsRunResults from './AiOpsRunResults.vue'
import AiOpsSessionList from './AiOpsSessionList.vue'
import type { Call, Confirmation } from '../../api/opsChat'
import en from '../../i18n/locales/en/opsChat'
import de from '../../i18n/locales/de/opsChat'
import es from '../../i18n/locales/es/opsChat'
import fr from '../../i18n/locales/fr/opsChat'
import ja from '../../i18n/locales/ja/opsChat'
import ko from '../../i18n/locales/ko/opsChat'
import zhCN from '../../i18n/locales/zh-CN/opsChat'
import zhTW from '../../i18n/locales/zh-TW/opsChat'
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
  it('has matching exec keys in all eight locales', () => {
    for (const locale of [en, de, es, fr, ja, ko, zhCN, zhTW]) {
      expect(Object.keys(locale.exec).sort()).toEqual(Object.keys(en.exec).sort())
      expect(Object.values(locale.exec).every((value) => value.length > 0)).toBe(true)
      expect(locale.tools.docker_exec).toBeTruthy()
    }
  })
  it('prepares literal docker_exec argv without action, risk or readonly fields', async () => {
    const w = mount(AiOpsComposer, {
      props: {
        draft: '', draftStatus: 'saved',
        tools: [{ toolId: 'host_resources', mutation: false, schema: {} }, { toolId: 'docker_exec', mutation: true, schema: {} }],
        disabled: false, active: false, modelAvailable: false, hasTargets: true, pending: false,
      },
    })
    try {
      w.getComponent(AppSelect).vm.$emit('update:modelValue', 'docker_exec')
      await w.vm.$nextTick()
      expect(w.findAllComponents(AppSelect)).toHaveLength(1)
      const prepare = w.get('.tool-fields > button')
      expect(prepare.text()).toBe('准备修改')
      expect(prepare.attributes('disabled')).toBeDefined()
      await w.get('input[aria-label="容器名称或 ID"]').setValue('web')
      const editor = w.getComponent(AiOpsExecArgs)
      await editor.get('input').setValue('sh')
      const tokens = ['-c', `printf '%s\\n' "quoted"; echo $(date) & <tag>; exit 0`, '  literal spaces  ', '']
      for (const [i, token] of tokens.entries()) {
        await editor.get('.exec-add').trigger('click')
        await editor.findAll('textarea')[i]!.setValue(token)
      }
      expect(prepare.attributes('disabled')).toBeUndefined()
      await prepare.trigger('click')
      expect(w.emitted('tool')).toEqual([['docker_exec', { container: 'web', argv: ['sh', ...tokens] }]])
      await w.setProps({ tools: [{ toolId: 'docker_exec', mutation: false, schema: {} }] })
      expect(prepare.text()).toBe('采集数据')
    } finally { w.unmount() }
  })
  it('blocks invalid or busy exec forms and keeps lifecycle actions separate', async () => {
    const w = mount(AiOpsComposer, {
      props: {
        draft: '', draftStatus: 'saved',
        tools: [{ toolId: 'docker_exec', mutation: true, schema: {} }, { toolId: 'docker_action', mutation: true, schema: {} }],
        disabled: false, active: false, modelAvailable: true, hasTargets: true, pending: false,
      },
    })
    try {
      w.getComponent(AppSelect).vm.$emit('update:modelValue', 'docker_exec')
      await w.vm.$nextTick()
      await w.get('input[aria-label="容器名称或 ID"]').setValue('web')
      const editor = w.getComponent(AiOpsExecArgs)
      const prepare = w.get('.tool-fields > button')
      for (const argv of [[''], ['  '], ['sh', '\u6d4b'.repeat(683)], ['sh', 'a'.repeat(2048), 'b'.repeat(2047)], ['sh', 'a\0b'], ['sh', 'a\nb'], ['sh', 'a\tb'], ['sh', '\u0085'], ['sh', '\ud800'], ['sh', '\udfff'], ['.'], ['/bin/..'], ['dir/.'], ['dir/sub/..'], Array<string>(62).fill('x')]) {
        editor.vm.$emit('update:argv', argv)
        await w.vm.$nextTick()
        expect(prepare.attributes('disabled')).toBeDefined()
        await prepare.trigger('click')
      }
      expect(w.emitted('tool')).toBeUndefined()
      editor.vm.$emit('update:argv', ['sh', '-c', 'echo ok'])
      await w.vm.$nextTick()
      for (const prop of ['disabled', 'active', 'pending'] as const) {
        await w.setProps({ [prop]: true })
        expect(prepare.attributes('disabled')).toBeDefined()
        expect(editor.get('input').attributes('disabled')).toBeDefined()
        await prepare.trigger('click')
        await w.setProps({ [prop]: false })
      }
      await w.setProps({ hasTargets: false })
      expect(prepare.attributes('disabled')).toBeDefined()
      await w.setProps({ hasTargets: true })
      w.findAllComponents(AppSelect)[0]!.vm.$emit('update:modelValue', 'docker_action')
      await w.vm.$nextTick()
      expect(w.findComponent(AiOpsExecArgs).exists()).toBe(false)
      expect(w.findAllComponents(AppSelect)).toHaveLength(2)
      await prepare.trigger('click')
      expect(w.emitted('tool')).toEqual([['docker_action', { container: 'web', action: 'restart' }]])
    } finally { w.unmount() }
  })
  it('displays and confirms full frozen exec argv as escaped text', async () => {
    const argv = ['sh', '-c', `echo "quoted"; <img src=x onerror=alert(1)>; ${'x'.repeat(1800)}`, '']
    const execCall: Call = { ...call, toolId: 'docker_exec', args: { container: call.object, argv } }
    const batch: Confirmation = { ...confirmation, calls: [{ ...confirmation.calls[0]!, toolId: 'docker_exec' }] }
    const w = mount(AiOpsConfirmation, { props: { confirmation: batch, calls: { c: execCall }, disabled: false } })
    try {
      expect(w.get('pre').text()).toBe(JSON.stringify(argv))
      expect(w.find('img').exists()).toBe(false)
      expect(w.text()).toContain(execCall.serverName)
      expect(w.text()).toContain(execCall.serverId)
      expect(w.text()).toContain(execCall.object)
      expect(w.text()).toContain('可能修改数据或访问秘密')
      expect(w.text()).toContain('不保证业务变更成功')
      expect(w.get('.ops-primary').attributes('disabled')).toBeUndefined()
      await w.get('.ops-primary').trigger('click')
      expect(w.emitted('confirm')).toEqual([[batch]])
    } finally { w.unmount() }
  })
  it.each(['toolId', 'callId', 'runId', 'serverId', 'object', 'argsHash', 'targetHash'] as const)(
    'blocks exec approval when the frozen %s binding differs', async (field) => {
      const execCall: Call = { ...call, toolId: 'docker_exec', args: { container: call.object, argv: ['sh', '-c', 'echo ok'] }, [field]: 'mismatch' }
      const batch: Confirmation = { ...confirmation, calls: [{ ...confirmation.calls[0]!, toolId: 'docker_exec' }] }
      const w = mount(AiOpsConfirmation, { props: { confirmation: batch, calls: { c: execCall }, disabled: false } })
      try {
        expect(w.get('.ops-primary').attributes('disabled')).toBeDefined()
        expect(w.find('pre').exists()).toBe(false)
        await w.get('.ops-primary').trigger('click')
        expect(w.emitted('confirm')).toBeUndefined()
      } finally { w.unmount() }
    },
  )
  it('requires argv for exec instead of guessing from a lifecycle action', async () => {
    const batch: Confirmation = { ...confirmation, calls: [{ ...confirmation.calls[0]!, toolId: 'docker_exec' }] }
    const w = mount(AiOpsConfirmation, { props: { confirmation: batch, calls: { c: { ...call, toolId: 'docker_exec' } }, disabled: false } })
    try {
      expect(w.get('.ops-primary').attributes('disabled')).toBeDefined()
      expect(w.find('pre').exists()).toBe(false)
      await w.setProps({ calls: { c: { ...call, toolId: 'docker_exec', args: { container: call.object, argv: [] } } } })
      expect(w.get('.ops-primary').attributes('disabled')).toBeDefined()
      await w.get('.ops-primary').trigger('click')
      expect(w.emitted('confirm')).toBeUndefined()
    } finally { w.unmount() }
  })
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
    await w.get('[aria-label="重命名"]').trigger('click')
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
    async (status) => {
      const w = mount(AiOpsToolResult, {
        props: {
          call: { ...call, status },
          disabled: false,
          modelAvailable: false,
        },
      })
      ;(w.get('details').element as HTMLDetailsElement).open=true
      await w.get('details').trigger('toggle')
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
    expect(w.find('pre').exists()).toBe(false)
    ;(w.get('details').element as HTMLDetailsElement).open=true
    await w.get('details').trigger('toggle')
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
      .find((b) => b.attributes('aria-label') === '复制')!
      .trigger('click')
    expect(w.emitted('copy')).toEqual([['rm -rf /example']])
    expect(w.emitted('execute')).toBeUndefined()
    w.unmount()
  })
  it('analysis needs no extra consent and never presents a paginated subset as a complete group', async () => {
    const w=mount(AiOpsRunResults,{props:{runId:'r',calls:[{...call,status:'succeeded'}],disabled:false,modelAvailable:true}})
    expect(w.text()).toContain('已加载项')
    expect(w.find('input').exists()).toBe(false)
    await w.get('[aria-label="分析已加载项"]').trigger('click')
    expect(w.emitted('analyze')).toEqual([[['c']]])
    await w.setProps({summary:{servers:[{serverId:'a',serverName:'A',missing:[],facts:[{callId:'c',toolId:'docker_logs',status:'succeeded',truncated:false},{callId:'unloaded',toolId:'host_processes',status:'succeeded',truncated:false}]}]}})
    expect(w.text()).toContain('2/2')
    expect(w.text()).not.toContain('已加载项')
    await w.get('[aria-label="分析此服务器结果"]').trigger('click')
    expect(w.emitted('analyze')?.at(-1)).toEqual([['c','unloaded']])
    await w.get('[aria-label="分析整轮结果"]').trigger('click')
    expect(w.emitted('analyzeRun')).toEqual([['r']])
    w.unmount()
  })
})
