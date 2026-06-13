package httpapi

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/audit"
)

// approval_link.go 实现「从通知直接审批」的**公开**端点(无会话;token 即认证)。
//
// 安全姿态(SECURITY-SENSITIVE):
//   - token 是 HMAC + 过期 + 常量时间比较签出的(见 internal/approval/link.go);任何持有有效
//     token 者即可代该审批门做批准/拒绝——故这两个端点注册在 requireAuth / CSRF 组**之外**。
//   - **GET 无任何副作用**:聊天/IM 客户端会预取链接(link preview),若 GET 即批准会被预取
//     自动放行。GET 仅校验 token 并渲染确认页;真正解析门只在 POST /approvals/act。
//   - 页面里所有动态值(runID/stageID)一律经 html/template 自动转义;页面/日志绝无 token、
//     绝无任何密钥。
//
// 路由(均公开,不过 auth、不过 CSRF):
//   - GET  /approvals?token=...   → 确认页(校验 + 展示 + 两个 POST 表单)
//   - POST /approvals/act         → 解析门(form: token, decision=approve|reject)

// approvalActor 是经签名链接审批时记入审计/决定的操作者标识(非登录会话)。
const approvalActor = "通知链接审批"

// approvalPageData 是确认页 / 结果页模板的渲染上下文(均为已转义文本)。
type approvalPageData struct {
	Title   string
	Message string
	// 以下仅确认页用。
	ShowForm bool
	Token    string // 注意:仅回填进隐藏表单字段(同源 POST 回来),不展示、不记录。
	RunID    string
	StageID  string
}

// approvalPageTmpl 是确认 / 结果 / 错误页共用的极简自包含 HTML 模板(无外链、无脚本)。
// 所有插值点(.Title/.Message/.RunID/.StageID/.Token)经 html/template 上下文感知转义。
var approvalPageTmpl = template.Must(template.New("approval").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: system-ui, -apple-system, "Segoe UI", sans-serif; margin: 0;
         min-height: 100vh; display: grid; place-items: center; background: #f4f5f7; color: #1f2329; }
  .card { background: #fff; border-radius: 12px; box-shadow: 0 8px 30px rgba(0,0,0,.08);
          padding: 2rem 2.25rem; max-width: 28rem; width: calc(100% - 2rem); }
  h1 { font-size: 1.25rem; margin: 0 0 1rem; }
  .meta { font-size: .9rem; color: #5b6168; margin: .25rem 0; word-break: break-all; }
  .meta b { color: #1f2329; font-weight: 600; }
  .msg { font-size: 1rem; margin: .5rem 0 0; }
  .actions { display: flex; gap: .75rem; margin-top: 1.5rem; }
  button { flex: 1; padding: .7rem 1rem; border: 0; border-radius: 8px; font-size: 1rem;
           cursor: pointer; font-weight: 600; }
  .approve { background: #1f9d55; color: #fff; }
  .reject  { background: #e5484d; color: #fff; }
  form { flex: 1; margin: 0; }
</style>
</head>
<body>
  <main class="card">
    <h1>{{.Title}}</h1>
    {{if .ShowForm}}
      <p class="meta">运行:<b>{{.RunID}}</b></p>
      <p class="meta">阶段:<b>{{.StageID}}</b></p>
      <p class="msg">{{.Message}}</p>
      <div class="actions">
        <form method="POST" action="/approvals/act">
          <input type="hidden" name="token" value="{{.Token}}">
          <input type="hidden" name="decision" value="approve">
          <button type="submit" class="approve">批准</button>
        </form>
        <form method="POST" action="/approvals/act">
          <input type="hidden" name="token" value="{{.Token}}">
          <input type="hidden" name="decision" value="reject">
          <button type="submit" class="reject">拒绝</button>
        </form>
      </div>
    {{else}}
      <p class="msg">{{.Message}}</p>
    {{end}}
  </main>
</body>
</html>`))

// renderApprovalPage 渲染一张审批页(状态码 + 模板)。渲染失败时退化为纯文本(绝不 panic)。
func renderApprovalPage(w http.ResponseWriter, status int, data approvalPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer") // 防 token 经 Referer 外泄。
	w.WriteHeader(status)
	if err := approvalPageTmpl.Execute(w, data); err != nil {
		_, _ = w.Write([]byte("操作完成,可关闭此页面。"))
	}
}

// makeApprovalPageHandler 返回 GET /approvals?token=... 的处理器(公开;无副作用)。
//
// token 无效/过期 → 友好错误页;门已不在等待 → 已处理页;否则 → 确认页(两个 POST 表单)。
func makeApprovalPageHandler(signer *approval.Signer, coord *approval.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if signer == nil || !signer.Enabled() || coord == nil {
			renderApprovalPage(w, http.StatusOK, approvalPageData{
				Title: "审批链接不可用", Message: "服务端未启用签名链接审批,请登录平台处理。",
			})
			return
		}
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		runID, stageID, ok := signer.Verify(token)
		if !ok {
			renderApprovalPage(w, http.StatusBadRequest, approvalPageData{
				Title: "链接无效", Message: "链接无效或已过期,请重新发起或登录平台处理。",
			})
			return
		}
		// GET 无副作用:仅查询门是否仍在等待。
		if !coord.IsWaiting(approval.Key(runID, stageID)) {
			renderApprovalPage(w, http.StatusOK, approvalPageData{
				Title: "无需处理", Message: "该运行已处理或链接已失效。",
			})
			return
		}
		renderApprovalPage(w, http.StatusOK, approvalPageData{
			Title:    "确认审批",
			Message:  "请确认是否放行该审批门。",
			ShowForm: true,
			Token:    token,
			RunID:    runID,
			StageID:  stageID,
		})
	}
}

// makeApprovalActHandler 返回 POST /approvals/act 的处理器(公开;token 即认证;唯一有副作用入口)。
//
// 解析 form 的 token + decision(approve|reject)→ Verify → coord.Resolve。Resolve 为 false
// (门已不在等待)→ 提示页;成功 → 审计(run.approve/run.reject)+ 结果页。
// **不**在此调 store.Decide——门内 worker 收到决定后自行落库(与 UI 审批端点一致)。
func makeApprovalActHandler(signer *approval.Signer, coord *approval.Coordinator, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if signer == nil || !signer.Enabled() || coord == nil {
			renderApprovalPage(w, http.StatusOK, approvalPageData{
				Title: "审批链接不可用", Message: "服务端未启用签名链接审批,请登录平台处理。",
			})
			return
		}
		// 限请求体大小(防滥用);仅解析表单字段。
		r.Body = http.MaxBytesReader(w, r.Body, 1<<13)
		if err := r.ParseForm(); err != nil {
			renderApprovalPage(w, http.StatusBadRequest, approvalPageData{
				Title: "请求无效", Message: "请求格式错误。",
			})
			return
		}
		token := strings.TrimSpace(r.PostFormValue("token"))
		decision := strings.TrimSpace(r.PostFormValue("decision"))
		runID, stageID, ok := signer.Verify(token)
		if !ok {
			renderApprovalPage(w, http.StatusBadRequest, approvalPageData{
				Title: "链接无效", Message: "链接无效或已过期,请重新发起或登录平台处理。",
			})
			return
		}
		if decision != "approve" && decision != "reject" {
			renderApprovalPage(w, http.StatusBadRequest, approvalPageData{
				Title: "请求无效", Message: "未指定有效的审批决定。",
			})
			return
		}
		approved := decision == "approve"
		key := approval.Key(runID, stageID)
		if !coord.Resolve(key, approval.Decision{Approved: approved, Actor: approvalActor}) {
			renderApprovalPage(w, http.StatusConflict, approvalPageData{
				Title: "无需处理", Message: "该运行已不在等待审批(可能已被处理或已超时)。",
			})
			return
		}
		action := "run.approve"
		title, msg := "已批准", "✅ 已批准,可关闭此页面。"
		if !approved {
			action = "run.reject"
			title, msg = "已拒绝", "❌ 已拒绝,可关闭此页面。"
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      approvalActor,
			Action:     action,
			TargetType: "run",
			TargetID:   runID,
			Detail:     map[string]any{"stageId": stageID, "via": "notification_link"},
			IP:         clientIP(r),
		})
		renderApprovalPage(w, http.StatusOK, approvalPageData{Title: title, Message: msg})
	}
}
