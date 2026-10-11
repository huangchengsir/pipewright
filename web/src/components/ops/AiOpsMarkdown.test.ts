import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AiOpsMarkdown from './AiOpsMarkdown.vue'
import AiOpsMessageList from './AiOpsMessageList.vue'

describe('safe AI Markdown', () => {
  it('maps frozen target aliases only in presentation text, never commands or URLs', async () => {
    const w = mount(AiOpsMarkdown, {
      props: {
        text: '## target_A\n\n服务器target_A的CPU\n\nBased on target_B and `target_A`.\n\n`ssh target_A`\n\n```sh\nssh target_A\n```\n\n[target_A](https://example.com/target_A)\n\nmy_target_A target_AB target_A1 target_Z',
        targetNames: {
          target_A: '测试',
          target_B: '<script>![x](https://x.invalid)</script>',
        },
      },
    })
    expect(w.get('h2').text()).toBe('测试')
    expect(w.text()).toContain('服务器测试的CPU')
    expect(w.text()).toContain('<script>![x](https://x.invalid)</script>')
    expect(w.find('script,img').exists()).toBe(false)
    expect(w.get('pre').text()).toBe('ssh target_A')
    expect(w.text()).toContain('my_target_A target_AB target_A1 target_Z')
    expect(w.get('a').text()).toBe('测试')
    expect(w.get('a').attributes('href')).toBe('https://example.com/target_A')
    await w.setProps({ targetNames: { target_A: '另一个冻结名称' } })
    expect(w.get('h2').text()).toBe('另一个冻结名称')
    w.unmount()
  })

  it('retains unmapped historical aliases and maps planning indexes independently', () => {
    const w = mount(AiOpsMarkdown, {
      props: {
        text: 'target_0 target_A',
        targetNames: { target_0: 'Server B' },
      },
    })
    expect(w.text()).toBe('Server B target_A')
    w.unmount()
  })
  it.each([40, 63, 65])(
    'handles %i quote levels without backtracking',
    (depth) => {
      const w = mount(AiOpsMarkdown, {
        props: { text: '> '.repeat(depth) + 'x' },
      })
      expect(w.text()).toContain('x')
      w.unmount()
    },
  )
  it('falls back safely for excessive nesting and decodes link entities', () => {
    const text = '> '.repeat(4000) + 'x'
    const w = mount(AiOpsMarkdown, { props: { text } })
    expect(w.text()).toBe(text)
    w.unmount()
    const link = mount(AiOpsMarkdown, {
      props: { text: '[logs](https://example.com/log?a=1&amp;b=2)' },
    })
    expect(link.get('a').attributes('href')).toBe(
      'https://example.com/log?a=1&b=2',
    )
    link.unmount()
  })
  it('renders headings, nested lists, tables, quotes and literal code', () => {
    const w = mount(AiOpsMarkdown, {
      props: {
        text: '## Server\n\n### Resources\n\n- **Memory**: 1 GiB\n  - Available\n\n| CPU | RAM |\n| --- | --- |\n| 1% | 2 GiB |\n\n> Evidence\n\n```sh\ndocker ps\n<img src=x onerror=alert(1)>\n```\n\n`a & b`',
      },
    })
    expect(w.get('h2').text()).toBe('Server')
    expect(w.get('strong').text()).toBe('Memory')
    expect(w.findAll('ul')).toHaveLength(2)
    expect(w.get('td').text()).toBe('1%')
    expect(w.get('blockquote').text()).toBe('Evidence')
    expect(w.get('pre code').text()).toContain('<img src=x onerror=alert(1)>')
    expect(w.find('img').exists()).toBe(false)
    w.unmount()
  })
  it('never inserts HTML, media, embedded content or unsafe links', () => {
    const w = mount(AiOpsMarkdown, {
      props: {
        text: '<script>alert(1)</script>\n\n<img src="https://secret.invalid/x" onerror="alert(1)">\n\n![remote](https://secret.invalid/log)\n\n[unsafe](javascript:alert(1)) [data](data:text/html,x) [credential](https://user:pass@example.com) [good](https://example.com/docs)',
      },
    })
    expect(
      w.find('script,img,iframe,svg,video,audio,object,embed').exists(),
    ).toBe(false)
    expect(w.findAll('a')).toHaveLength(1)
    expect(w.get('a').attributes('rel')).toBe('noopener noreferrer')
    expect(w.get('a').attributes('href')).toBe('https://example.com/docs')
    expect(w.text()).toContain('remote')
    w.unmount()
  })
  it('updates incomplete streaming Markdown without executing fenced HTML', async () => {
    const w = mount(AiOpsMarkdown, { props: { text: '## Server\n\n**Mem' } })
    expect(w.get('h2').text()).toBe('Server')
    await w.setProps({
      text: '## Server\n\n**Memory**\n\n```html\n<script>alert(1)',
    })
    expect(w.get('strong').text()).toBe('Memory')
    expect(w.get('pre').text()).toContain('<script>')
    expect(w.find('script').exists()).toBe(false)
    w.unmount()
  })
  it('leaves user messages plain and copies the original AI Markdown', async () => {
    const text = '## Resources\n\n**Memory**'
    const w = mount(AiOpsMessageList, {
      props: {
        entries: [
          {
            sessionId: 's',
            seq: 1,
            kind: 'user',
            text: '**not formatting**',
            createdAt: '',
          },
          {
            sessionId: 's',
            seq: 2,
            kind: 'analysis',
            status: 'succeeded',
            text,
            createdAt: '',
          },
        ],
        calls: {},
        disabled: false,
        modelAvailable: true,
        hasHistory: false,
      },
    })
    expect(w.get('.message.user').text()).toBe('**not formatting**')
    expect(w.get('h2').text()).toBe('Resources')
    await w.get('button[title="复制"]').trigger('click')
    expect(w.emitted('copy')?.[0]?.[0]).toBe(text)
    w.unmount()
  })
})
