import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import MCPClientConfig from './MCPClientConfig.vue'

const endpoint = 'http://127.0.0.1:18090/mcp'

describe('MCP client configurations', () => {
  it('selects one client at a time while retaining both exact file formats', () => {
    const w = mount(MCPClientConfig, { props: { endpoint } })
    expect(w.get('details').attributes('open')).toBeDefined()
    expect(w.get('[data-client-tab="codex"]').attributes('aria-selected')).toBe('true')
    expect(w.get('[data-client="codex"]').attributes('hidden')).toBeUndefined()
    expect(w.get('[data-client="claude"]').attributes('hidden')).toBeDefined()
    const codex = w.get('[data-client="codex"] pre').text()
    expect(codex.split(String.fromCharCode(10))).toHaveLength(3)
    expect(codex).not.toContain(String.fromCharCode(92) + 'n')
    expect(w.get('[data-client="claude"]').text()).toContain('.mcp.json')
    expect(w.get('[data-client="codex"]').text()).toContain('~/.codex/config.toml')
    expect(JSON.parse(w.get('[data-client="claude"] pre').text())).toEqual({
      mcpServers: { pipewright: { type: 'http', url: endpoint, headers: { Authorization: 'Bearer <MCP_TOKEN>' } } },
    })
    expect(w.get('[data-client="codex"] pre').text()).toBe(
      `[mcp_servers.pipewright]\nurl = "${endpoint}"\nhttp_headers = { Authorization = "Bearer <MCP_TOKEN>" }`,
    )
    w.unmount()
  })

  it.each(['claude', 'codex'])('copies only the %s configuration', async (client) => {
    const w = mount(MCPClientConfig, { props: { endpoint } })
    await w.get(`[data-client-tab="${client}"]`).trigger('click')
    const panel = w.get(`[data-client="${client}"]`)
    expect(panel.attributes('hidden')).toBeUndefined()
    const config = panel.get('pre').text()
    expect(panel.get('button').attributes('aria-label')).toContain(client === 'claude' ? 'Claude Code' : 'Codex')
    await panel.get('button').trigger('click')
    expect(w.emitted('copy')).toEqual([[config]])
    w.unmount()
  })


  it('links tabs and panels and supports roving focus with arrows, Home and End', async () => {
    const w = mount(MCPClientConfig, { props: { endpoint }, attachTo: document.body })
    const codex = w.get('[data-client-tab="codex"]')
    const claude = w.get('[data-client-tab="claude"]')
    for (const tab of [codex, claude]) {
      const panel = w.get(`#${tab.attributes('aria-controls')}`)
      expect(panel.attributes('aria-labelledby')).toBe(tab.attributes('id'))
    }
    await codex.trigger('keydown', { key: 'ArrowRight' }); await flushPromises()
    expect(document.activeElement).toBe(claude.element)
    expect(claude.attributes('aria-selected')).toBe('true')
    expect(codex.attributes('tabindex')).toBe('-1')
    await claude.trigger('keydown', { key: 'ArrowRight' }); await flushPromises()
    expect(codex.attributes('aria-selected')).toBe('true')
    await codex.trigger('keydown', { key: 'End' }); await flushPromises()
    expect(claude.attributes('aria-selected')).toBe('true')
    await claude.trigger('keydown', { key: 'Home' }); await flushPromises()
    expect(codex.attributes('aria-selected')).toBe('true')
    await codex.trigger('keydown', { key: 'ArrowLeft' }); await flushPromises()
    expect(claude.attributes('aria-selected')).toBe('true')
    w.unmount()
  })

  it('updates both examples when the endpoint changes without network or stored credentials', async () => {
    const w = mount(MCPClientConfig, { props: { endpoint } })
    const other = 'https://pipewright.example.com:8443/mcp'
    await w.setProps({ endpoint: other })
    expect(w.findAll('pre')).toHaveLength(2)
    for (const pre of w.findAll('pre')) {
      expect(pre.text()).toContain(other)
      expect(pre.text()).toContain('<MCP_TOKEN>')
      expect(pre.text()).not.toContain(endpoint)
    }
    w.unmount()
  })
})
