import { expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import type { Call } from '../../api/opsChat'
import AiOpsToolResult from './AiOpsToolResult.vue'

it('shows exact container argv as inert text and never offers command replay', async () => {
  const argv = [
    'sh',
    '-c',
    "printf '%s' '<img src=x onerror=alert(1)>; $(id)'",
    '',
  ]
  const call: Call = {
    callId: 'exec',
    runId: 'run',
    sessionId: 'chat',
    serverId: 'a',
    serverName: 'Frozen A',
    toolId: 'docker_exec',
    args: { container: 'a'.repeat(64), argv },
    object: 'a'.repeat(64),
    status: 'failed',
    argsHash: 'args',
    targetHash: 'target',
    output: 'command output',
    error: 'nonzero exit',
    truncated: false,
    exitCode: 1,
  }
  const wrapper = mount(AiOpsToolResult, {
    props: { call, disabled: false, modelAvailable: true },
  })
  const details = wrapper.get('details')
  ;(details.element as HTMLDetailsElement).open = true
  await details.trigger('toggle')
  expect(wrapper.get('.exec-argv').text()).toBe(JSON.stringify(argv))
  expect(wrapper.find('img').exists()).toBe(false)
  expect(wrapper.find('[aria-label="仅重试此失败调用"]').exists()).toBe(false)
  expect(wrapper.find('[aria-label="交给 AI 分析"]').exists()).toBe(true)
  wrapper.unmount()
})
