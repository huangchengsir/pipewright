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

func toRunDetailDTO(r *run.Run) runDetailDTO {
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
		Targets:     nil,       // Epic 4 填;形状冻结
		Diagnosis:   diagnosis, // Story 7.2 填(无诊断 → nil);形状冻结
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
		writeJSON(w, http.StatusOK, toRunDetailDTO(rn))
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
		writeJSON(w, http.StatusOK, toRunDetailDTO(rn))
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
