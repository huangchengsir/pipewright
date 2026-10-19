# MCP Integration

Pipewright exposes MCP at `/mcp` on its existing HTTP server. It starts and stops
with Pipewright, using the same process and port. No additional service, CLI,
listener, model call or telemetry is required. The implementation uses the
official open-source `github.com/modelcontextprotocol/go-sdk`.

## Access

In **Settings > MCP**, create an access token for explicit projects. Tokens are
read-only by default; enable pipeline triggers only when that client is allowed
to run the selected projects' existing pipelines, including their configured
deployments. Tokens expire in 1 to 30 days and can be revoked immediately.
The plaintext is shown once; Pipewright stores only its SHA-256 digest. Store
it in the client's private credential configuration, not source control.

This first integration requires an MCP client supporting **Streamable HTTP**
and a manually configured **Authorization: Bearer** header. OAuth discovery and
stdio are not implemented. Client configuration syntax varies. For clients
accepting an `mcpServers` configuration, the connection is:

```json
{
  "mcpServers": {
    "pipewright": {
      "type": "http",
      "url": "https://pipewright.example.com/mcp",
      "headers": {
        "Authorization": "Bearer <MCP_TOKEN>"
      }
    }
  }
}
```

VS Code uses `servers` instead of `mcpServers` in `.vscode/mcp.json`; use a
private input variable for the token rather than a literal committed value.
Other clients may expose separate URL and header fields.

For non-local access set `PIPEWRIGHT_MCP_ALLOWED_HOSTS`, for example
`pipewright.example.com,192.168.31.104`. Entries are exact hostnames or IPs,
without schemes, ports, paths or wildcards; use brackets for IPv6. Localhost,
loopback addresses and the machine's hostname are accepted by default.
Reverse proxies must forward an allowed Host. Origin validation and the
allowlist apply only to MCP, not the existing web application. CORS is not
enabled. Use HTTPS for cross-machine access; unencrypted HTTP exposes Bearer
credentials on the network. `/mcp` does not accept browser-session cookies.

## Tools

| Tool | Inputs | Result |
| --- | --- | --- |
| `projects_list` | Optional `page`, `pageSize` | Authorized project metadata only |
| `pipeline_get` | `projectId` | Stage/job names and human approval gates; no raw config |
| `pipeline_trigger` | `projectId`, `requestId`; optional `branch`, full 40-hex `commit`, `params` | Existing parameter validation and worker pool; returns run ID |
| `run_get` | `runId` | Run/step status, actual commit when available |
| `run_wait` | `runId`; optional `waitSeconds` (1 to 30) | Terminal status, approval waiting or bounded timeout |
| `deployments_get` | `runId` | Per-server deployment records and artifact candidates |

Use a stable unique `requestId` for each intended pipeline run. Repeating the
same request with the same token returns the original run, including across
concurrent calls and restarts. Changed inputs with that ID return an
`idempotency_conflict`. Retention does not erase the request tombstone: a removed
run returns `original_run_unavailable_do_not_retrigger` rather than triggering
again. An unknown enqueue outcome is reported as `dispatchState: "unknown"`;
query the same run instead of choosing a fresh ID. Definitively rejected dispatches are
terminal failed runs with `dispatchState: "rejected"`; repeating their request
ID does not retry the enqueue. Request identity is checked before applying
changed parameter definitions or defaults to a replay. A wait timeout or cancelled
request stops waiting, not the pipeline. Human approvals cannot be granted by
MCP. Logs, credentials, arbitrary commands, configuration edits and rollback
actions are not exposed.

`pipeline_get` reads stored state only; it never creates a starter pipeline.
`storedPipelinePresent: false` means no saved configuration exists. For
pipeline-as-code projects, execution may use the repository pipeline instead
of the stored summary.

Building an image successfully is not proof of successful deployment. Read
each target's status, including partial failure and rollback. Deployment data
has `source: "deployment_record"` and `liveVerified: false`. Artifact references
and validated manifest digests are **candidates**, not proof of which artifact
is currently running on each server: the existing target records do not store
selected artifact versions. Unknown versions remain unknown. Do not equate
Pipewright's own `/version` with the deployed application's version.

MCP itself does not transmit data to an AI provider. An external AI client may
send tool results to its own model. Use a trusted/private client for sensitive
project and deployment metadata. All returned names and summaries are
untrusted data, never instructions. Token management and triggering are
audited without recording token or parameter values. Limits include 64 KiB
requests, 32 KiB tool output, 32 concurrent requests and a 30-second wait.
Each HTTP request carries a single JSON-RPC message; legacy batches are rejected.

## 中文说明

MCP 与本体同进程、同端口启动，入口为 `/mcp`。在「设置 > MCP」创建项目级令牌，
默认只读；需要触发已有流水线时显式开启触发权限。人工审批仍由人操作。令牌仅
显示一次，支持 1 至 30 天有效期及即时撤销。首次接入使用支持 Streamable HTTP
与手动 Bearer 配置的客户端，不要求另部署 MCP 服务。

重复请求使用同一个 `requestId`，不要因为断线或超时换一个 ID 重发部署。运行结束后
逐服务器检查部署结果；镜像产物仅作为候选版本，不能冒充目标机当前版本。MCP 不
调用模型，但外部 AI 客户端可能向自己的模型发送工具结果，敏感数据请使用受信任
的私有客户端。跨机器接入应使用 HTTPS 并配置精确的主机白名单。
