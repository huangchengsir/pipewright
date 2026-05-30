package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/run"
)

// maxPage 是列表分页 page 的上界:防极大 page 致 (page-1)*size OFFSET 溢出。
const maxPage = 100000

// runSubscriber 抽象「订阅单个 run 的状态/步骤事件」的能力(由 worker pool 实现)。
// SSE handler 据此推送,无需轮询 DB(避免 SetMaxOpenConns(1) 下长连接占用唯一连接)。
type runSubscriber interface {
	Subscribe(runID string) (<-chan run.Event, func())
}

// ---- 冻结 run-detail DTO 契约 ------------------------------------------
//
// 形状由 Story 3.1 定死;Epic 4(targets)/Epic 7(diagnosis)只往 null 块填值、
// 不得改字段名/层级/可选性。本期 targets/diagnosis 恒为 null。

// runDetailDTO 是 GET /api/runs/{id} 响应体(冻结)。
type runDetailDTO struct {
	ID          string      `json:"id"`
	ProjectID   string      `json:"projectId"`
	ProjectName string      `json:"projectName"`
	Status      string      `json:"status"`
	Trigger     triggerInfo `json:"trigger"`
	Steps       []stepDTO   `json:"steps"`
	CreatedAt   string      `json:"createdAt"`
	StartedAt   *string     `json:"startedAt"`
	FinishedAt  *string     `json:"finishedAt"`
	DurationMs  *int64      `json:"durationMs"`
	// artifacts:Story 3.4 填(构建产物契约 / FR-6);成功/有产物时为数组,否则 []。形状冻结。
	Artifacts []artifactDTO `json:"artifacts"`
	// targets:Epic 4 填(多机扇出);本期恒 null。形状冻结,不在本期建模。
	Targets *any `json:"targets"`
	// diagnosis:Epic 7 填(AI 诊断);本期恒 null。形状冻结,不在本期建模。
	Diagnosis *any `json:"diagnosis"`
}

type triggerInfo struct {
	Type   string `json:"type"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Actor  string `json:"actor"`
}

type stepDTO struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	StartedAt  *string `json:"startedAt"`
	FinishedAt *string `json:"finishedAt"`
	DurationMs *int64  `json:"durationMs"`
}

// runListItemDTO 是 GET /api/runs 列表行(精简;不含 steps/targets/diagnosis)。
type runListItemDTO struct {
	ID          string      `json:"id"`
	ProjectID   string      `json:"projectId"`
	ProjectName string      `json:"projectName"`
	Status      string      `json:"status"`
	Trigger     triggerInfo `json:"trigger"`
	CreatedAt   string      `json:"createdAt"`
	DurationMs  *int64      `json:"durationMs"`
}

type runListDTO struct {
	Items []runListItemDTO `json:"items"`
	Page  int              `json:"page"`
	Total int              `json:"total"`
}

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// durationMs 计算 [start,end] 毫秒时长;任一为 nil → nil。
func durationMs(start, end *time.Time) *int64 {
	if start == nil || end == nil {
		return nil
	}
	ms := end.Sub(*start).Milliseconds()
	return &ms
}

func toTriggerInfo(t run.Trigger) triggerInfo {
	return triggerInfo{Type: t.Type, Branch: t.Branch, Commit: t.Commit, Actor: t.Actor}
}

// toRunDetailDTO 映射运行到冻结 run-detail DTO。artifacts 填 Story 3.4 的产物 slot
// (成功/有产物时非空,否则 []);刚创建/进行中的运行传 nil(尚无产物 → [])。
func toRunDetailDTO(r *run.Run, artifacts []run.Artifact) runDetailDTO {
	steps := make([]stepDTO, 0, len(r.Steps))
	for i := range r.Steps {
		st := r.Steps[i]
		steps = append(steps, stepDTO{
			ID:         st.ID,
			Name:       st.Name,
			Status:     st.Status,
			StartedAt:  rfc3339Ptr(st.StartedAt),
			FinishedAt: rfc3339Ptr(st.FinishedAt),
			DurationMs: durationMs(st.StartedAt, st.FinishedAt),
		})
	}
	// diagnosis:Story 7.2 填(失败且已诊断时非 null;否则 null)。冻结子 DTO 形状。
	var diagnosis *any
	if dto := toDiagnosisDTO(r.Diagnosis); dto != nil {
		var v any = dto
		diagnosis = &v
	}

	return runDetailDTO{
		ID:          r.ID,
		ProjectID:   r.ProjectID,
		ProjectName: r.ProjectName,
		Status:      r.Status,
		Trigger:     toTriggerInfo(r.Trigger),
		Steps:       steps,
		CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
		StartedAt:   rfc3339Ptr(r.StartedAt),
		FinishedAt:  rfc3339Ptr(r.FinishedAt),
		DurationMs:  durationMs(r.StartedAt, r.FinishedAt),
		Artifacts:   toArtifactDTOs(artifacts), // Story 3.4 填(FR-6);无产物 → []
		Targets:     nil,                       // Epic 4 填;形状冻结
		Diagnosis:   diagnosis,                 // Story 7.2 填(无诊断 → nil);形状冻结
	}
}

func toRunListItemDTO(r *run.Run) runListItemDTO {
	return runListItemDTO{
		ID:          r.ID,
		ProjectID:   r.ProjectID,
		ProjectName: r.ProjectName,
		Status:      r.Status,
		Trigger:     toTriggerInfo(r.Trigger),
		CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
		DurationMs:  durationMs(r.StartedAt, r.FinishedAt),
	}
}

// writeRunError 把领域错误映射为契约错误码/状态码。
func writeRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, run.ErrNotFound):
		writeError(w, http.StatusNotFound, "run_not_found", "运行不存在")
	case errors.Is(err, run.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, run.ErrNotCancelable):
		writeError(w, http.StatusConflict, "run_not_cancelable", "运行已结束,无法取消")
	case errors.Is(err, run.ErrInvalidStatus):
		writeError(w, http.StatusBadRequest, "invalid_status", "状态筛选值非法")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeListRunsHandler 返回 GET /api/runs handler(筛选 projectId/status + 分页 page）。
func makeListRunsHandler(svc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		q := r.URL.Query()
		page := 1
		if v := q.Get("page"); v != "" {
			p, err := strconv.Atoi(v)
			if err != nil || p < 1 {
				writeError(w, http.StatusBadRequest, "invalid_page", "page 必须为不小于 1 的整数")
				return
			}
			if p > maxPage {
				writeError(w, http.StatusBadRequest, "invalid_page", "page 超出上界")
				return
			}
			page = p
		}
		res, err := svc.List(r.Context(), run.ListFilter{
			ProjectID: q.Get("projectId"),
			Status:    q.Get("status"),
			Page:      page,
		})
		if err != nil {
			writeRunError(w, err)
			return
		}
		items := make([]runListItemDTO, 0, len(res.Items))
		for i := range res.Items {
			items = append(items, toRunListItemDTO(&res.Items[i]))
		}
		writeJSON(w, http.StatusOK, runListDTO{Items: items, Page: res.Page, Total: res.Total})
	}
}

// makeGetRunHandler 返回 GET /api/runs/{id} handler(冻结 run-detail DTO;targets/diagnosis=null）。
func makeGetRunHandler(svc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		rn, err := svc.Get(r.Context(), id)
		if err != nil {
			writeRunError(w, err)
			return
		}
		// 填 Story 3.4 产物 slot(成功/有产物时非空,否则 [])。读失败不阻断详情(降级为空产物)。
		artifacts, aerr := svc.ListArtifacts(r.Context(), id)
		if aerr != nil {
			artifacts = nil
		}
		writeJSON(w, http.StatusOK, toRunDetailDTO(rn, artifacts))
	}
}

// makeCancelRunHandler 返回 POST /api/runs/{id}/cancel handler(过 CSRF;返回更新后 run）。
func makeCancelRunHandler(svc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		rn, err := svc.Cancel(r.Context(), id)
		if err != nil {
			writeRunError(w, err)
			return
		}
		// 取消返回的是进行中→终态(failed)快照,通常无产物;nil → [](仿成功路径 slot 形状)。
		writeJSON(w, http.StatusOK, toRunDetailDTO(rn, nil))
	}
}

// makeRunEventsHandler 返回 GET /api/runs/{id}/events SSE handler。
// 推 event: status(run 状态)与 event: step(步骤状态)JSON;仅状态,不含日志正文(=3-6)。
// 经事件总线订阅(不轮询 DB);心跳保活;客户端断连/服务停机即清理订阅。
func makeRunEventsHandler(svc run.Service, sub runSubscriber) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || sub == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "internal", "服务器不支持流式响应")
			return
		}

		// 先订阅、再取快照(顺序关键):订阅注册早于快照,确保快照与订阅之间产生的事件
		// (含终态 status)不会漏掉;否则"先 Get 后 Subscribe"窗口内的终态会丢失致永久挂起。
		events, cancel := sub.Subscribe(id)
		defer cancel()

		// 订阅就绪后取当前快照(降级:无 SSE 也可轮询此快照)。run 不存在则 404。
		rn, err := svc.Get(r.Context(), id)
		if err != nil {
			cancel() // 释放订阅;尚未写任何 SSE 帧,可正常返回错误状态码。
			writeRunError(w, err)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// 连接即回放(Story 3.6):先按 seq 升序补发该 run 全部历史 log 事件(已脱敏,从 DB 读
		// 不全量驻留内存),让刷新者拿到完整日志,再转入实时。订阅已在快照前注册,故回放窗口内
		// 新产生的行也会经实时通道补达(前端按 seq 去重)。
		replaySeq := 0
		if hist, herr := svc.GetLogs(r.Context(), id, 0); herr == nil {
			for i := range hist {
				writeSSE(w, flusher, string(run.EventLog), logPayload(id, hist[i]))
				replaySeq = hist[i].Seq
			}
		}

		// 初始 status 事件:让客户端立刻拿到当前态(进行中据此渲染)。
		writeSSE(w, flusher, string(run.EventStatus), statusPayload(id, rn.Status))
		// 若快照已终态:发完初始帧立即收流(无后续事件)。
		if run.IsTerminal(rn.Status) {
			return
		}

		ctx := r.Context()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				// 心跳:保活 + 探测断连(写失败则 handler 退出)。
				if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
					return
				}
				flusher.Flush()
				// 兜底:慢/不读订阅者会被非阻塞 bus 丢弃事件(含终态),致 SSE 永不结束。
				// 每次心跳时主动查一次 run 状态,已终态则补发终态帧并收流(不改 bus 非阻塞语义)。
				if cur, gerr := svc.Get(ctx, id); gerr == nil && run.IsTerminal(cur.Status) {
					writeSSE(w, flusher, string(run.EventStatus), statusPayload(id, cur.Status))
					return
				}
			case ev, more := <-events:
				if !more {
					return
				}
				switch ev.Kind {
				case run.EventStatus:
					writeSSE(w, flusher, string(run.EventStatus), statusPayload(ev.RunID, ev.Status))
					if run.IsTerminal(ev.Status) {
						return // 终态推完即结束流。
					}
				case run.EventStep:
					writeSSE(w, flusher, string(run.EventStep), stepPayload(ev.RunID, ev.Step))
				case run.EventLog:
					// 去重:回放已补发 seq<=replaySeq 的历史行(回放与订阅窗口可能重叠)。
					if ev.Log.Seq <= replaySeq {
						continue
					}
					replaySeq = ev.Log.Seq
					writeSSE(w, flusher, string(run.EventLog), logPayload(ev.RunID, ev.Log))
				}
			}
		}
	}
}

// writeSSE 写一帧 SSE 事件(event: <name>\n data: <json>\n\n)并 flush。
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

// statusPayload 构造 status 事件 JSON(经 json.Marshal,安全转义)。
func statusPayload(runID, status string) string {
	b, _ := json.Marshal(map[string]string{"runId": runID, "status": status})
	return string(b)
}

// stepPayload 构造 step 事件 JSON(仅状态元数据,不含日志正文=3-6)。
func stepPayload(runID string, st run.Step) string {
	b, _ := json.Marshal(map[string]any{
		"runId":   runID,
		"id":      st.ID,
		"name":    st.Name,
		"status":  st.Status,
		"ordinal": st.Ordinal,
	})
	return string(b)
}

// ---- 冻结 log 契约(Story 3.6;camelCase;text 已脱敏) ----------------------

// logLineDTO 是单行日志(SSE log 事件负载 + logs 拉取 DTO 共用形状)。
type logLineDTO struct {
	Seq         int    `json:"seq"`
	Ts          string `json:"ts"`
	Stream      string `json:"stream"`
	StepOrdinal int    `json:"stepOrdinal"`
	Text        string `json:"text"`
}

// runLogsDTO 是 GET /api/runs/{id}/logs 响应体(冻结)。
type runLogsDTO struct {
	Lines    []logLineDTO `json:"lines"`
	NextSeq  int          `json:"nextSeq"`
	Complete bool         `json:"complete"`
}

func toLogLineDTO(l run.LogLine) logLineDTO {
	return logLineDTO{
		Seq:         l.Seq,
		Ts:          l.Ts.UTC().Format(time.RFC3339),
		Stream:      l.Stream,
		StepOrdinal: l.StepOrdinal,
		Text:        l.Text,
	}
}

// logPayload 构造 log SSE 事件 JSON(已脱敏文本;经 json.Marshal 安全转义)。
func logPayload(_ string, l run.LogLine) string {
	b, _ := json.Marshal(toLogLineDTO(l))
	return string(b)
}

// makeRunLogsHandler 返回 GET /api/runs/{id}/logs handler(认证;非 SSE 历史拉取 / 分页)。
// 查询 ?sinceSeq=<int>(默认 0)→ 返回该 seq 之后的行(升序);nextSeq=末行 seq+1;
// complete=run 是否已终态(前端据此停止轮询)。run 不存在 → 404 run_not_found。
func makeRunLogsHandler(svc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		// run 存在性 + 终态:不存在 → 404(GetLogs 对不存在 run 返回空,不报错)。
		rn, err := svc.Get(r.Context(), id)
		if err != nil {
			writeRunError(w, err)
			return
		}

		sinceSeq := 0
		if v := r.URL.Query().Get("sinceSeq"); v != "" {
			n, perr := strconv.Atoi(v)
			if perr != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid_since_seq", "sinceSeq 必须为不小于 0 的整数")
				return
			}
			sinceSeq = n
		}

		lines, err := svc.GetLogs(r.Context(), id, sinceSeq)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		dto := runLogsDTO{
			Lines:    make([]logLineDTO, 0, len(lines)),
			NextSeq:  sinceSeq,
			Complete: run.IsTerminal(rn.Status),
		}
		for i := range lines {
			dto.Lines = append(dto.Lines, toLogLineDTO(lines[i]))
		}
		if n := len(lines); n > 0 {
			dto.NextSeq = lines[n-1].Seq + 1
		}
		writeJSON(w, http.StatusOK, dto)
	}
}
