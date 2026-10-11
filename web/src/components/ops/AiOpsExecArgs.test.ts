import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AiOpsExecArgs from './AiOpsExecArgs.vue'
import { isValidExecArgv } from '../../api/opsChat'

let wrapper: VueWrapper<InstanceType<typeof AiOpsExecArgs>> | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('literal exec tokens', () => {
  it('emits new arrays without parsing quotes, spaces, metacharacters or scripts', async () => {
    const argv = Object.freeze(['sh', '-c', 'old']) as unknown as string[]
    wrapper = mount(AiOpsExecArgs, { props: { argv, disabled: false } })
    const script = `printf '%s\\n' "literal quotes"; echo $(whoami) & <tag>\nexit 0`
    await wrapper.findAll('textarea')[1]!.setValue(script)
    const update = wrapper.emitted('update:argv')![0]![0] as string[]
    expect(update).toEqual(['sh', '-c', script])
    expect(update).not.toBe(argv)
    expect(argv).toEqual(['sh', '-c', 'old'])
    expect(wrapper.findAll('label')).toHaveLength(3)
    expect(wrapper.get('.exec-program').attributes('id')).toBeDefined()
    await wrapper.setProps({ argv: update })
    expect((wrapper.findAll('textarea')[1]!.element as HTMLTextAreaElement).value).toBe(script)
  })

  it('adds and removes individual tokens and preserves empty or whitespace arguments', async () => {
    wrapper = mount(AiOpsExecArgs, { props: { argv: ['printf', '', '  '], disabled: false } })
    await wrapper.get('.exec-add').trigger('click')
    expect(wrapper.emitted('update:argv')?.[0]).toEqual([['printf', '', '  ', '']])
    await wrapper.get('.exec-token-row button').trigger('click')
    expect(wrapper.emitted('update:argv')?.[1]).toEqual([['printf', '  ']])
    expect(wrapper.props('argv')).toEqual(['printf', '', '  '])
  })

  it('blocks every update while disabled and caps additions at 61 argv tokens', async () => {
    wrapper = mount(AiOpsExecArgs, { props: { argv: ['sh', '-c'], disabled: true } })
    expect(wrapper.findAll('input, textarea, button').every((control) => control.attributes('disabled') !== undefined)).toBe(true)
    await wrapper.get('input').setValue('other')
    await wrapper.get('textarea').setValue('changed')
    await wrapper.get('.exec-add').trigger('click')
    await wrapper.get('.exec-token-row button').trigger('click')
    expect(wrapper.emitted('update:argv')).toBeUndefined()
    await wrapper.setProps({ disabled: false, argv: ['program', ...Array<string>(60).fill('')] })
    expect(wrapper.get('.exec-add').attributes('disabled')).toBeDefined()
    await wrapper.get('.exec-add').trigger('click')
    expect(wrapper.emitted('update:argv')).toBeUndefined()
  })
})

describe('exec argv validation', () => {
  it.each([undefined, null, 'sh -c text', [], [''], ['  '], ['sh', 1], ['sh', null], ['sh', 'nul\0byte'], ['sh', 'line\nbreak'], ['sh', 'tab\tvalue'], ['sh', '\u007f'], ['sh', '\u0085'], ['sh', '\u009f'], ['.'], ['..'], ['/'], ['bin/'], ['/bin/..'], ['a/..'], ['-sh'], ['+sh'], ['sh -c'], ['\u7a0b\u5e8f'], Array<string>(62).fill('a')])(
    'rejects missing, malformed or out-of-count argv %#', (argv) => {
      expect(isValidExecArgv(argv)).toBe(false)
    },
  )
  it('accepts exact byte and count limits, including literal empty arguments', () => {
    expect(isValidExecArgv(['sh', ...Array<string>(60).fill('')])).toBe(true)
    expect(isValidExecArgv(['sh', 'a'.repeat(2048), 'b'.repeat(2046)])).toBe(true)
    expect(isValidExecArgv(['sh', 'a'.repeat(2049)])).toBe(false)
    expect(isValidExecArgv(['sh', 'a'.repeat(2048), 'b'.repeat(2047)])).toBe(false)
    expect(isValidExecArgv(['sh', '\u6d4b'.repeat(682) + 'aa'])).toBe(true)
    expect(isValidExecArgv(['sh', '\u6d4b'.repeat(683)])).toBe(false)
    expect(isValidExecArgv(['sh', '-c', 'echo "quoted"; echo $(date); exit 0'])).toBe(true)
    for (const program of ['/bin/sh', './program', '../program', 'name+-', 'dir/../program', '.././program']) {
      expect(isValidExecArgv([program])).toBe(true)
    }
  })
  it.each(['dir/.', 'dir/sub/..'])('rejects a final literal dot segment in %s', (program) => {
    expect(isValidExecArgv([program])).toBe(false)
  })
  it.each(['\ud800', '\udfff', 'a\ud800b', 'a\udfffb', '\udfff\ud800'])(
    'rejects actual lone UTF-16 surrogates %#', (token) => {
      expect(isValidExecArgv(['sh', token])).toBe(false)
    },
  )
  it('accepts valid surrogate pairs and measures their UTF-8 bytes exactly', () => {
    const pair = '\ud83d\ude00'
    expect(isValidExecArgv(['sh', pair])).toBe(true)
    expect(isValidExecArgv(['sh', pair.repeat(512), 'a'.repeat(2046)])).toBe(true)
    expect(isValidExecArgv(['sh', pair.repeat(513)])).toBe(false)
    expect(isValidExecArgv(['sh', pair.repeat(512), 'a'.repeat(2047)])).toBe(false)
  })
})
