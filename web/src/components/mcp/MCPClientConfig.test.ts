import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import MCPClientConfig from './MCPClientConfig.vue'

const endpoint = 'http://127.0.0.1:18090/mcp'

describe('MCP client configurations', () => {
  it('shows both clients by default with their distinct file formats', () => {
    const w = mount(MCPClientConfig, { props: { endpoint } })
    expect(w.get('details').attributes('open')).toBeDefined()
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
    const panel = w.get(`[data-client="${client}"]`)
    const config = panel.get('pre').text()
    expect(panel.get('button').attributes('aria-label')).toContain(client === 'claude' ? 'Claude Code' : 'Codex')
    await panel.get('button').trigger('click')
    expect(w.emitted('copy')).toEqual([[config]])
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
