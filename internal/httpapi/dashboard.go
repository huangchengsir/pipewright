package httpapi

import (
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"sort"
	"sync"
)

// Dashboard sections preserve independent loading/error states. Every section
// uses the same authenticated context, then its own module permission. Project
// scoped reads additionally intersect dashboard.read before SQL aggregation.
func makeDashboardHandler(o *options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if o.configDB == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "概览服务未初始化")
			return
		}
		ctx, err := tenancy.WithDashboardScope(r.Context(), o.configDB)
		if err != nil {
			accessError(w, err)
			return
		}
		r = r.WithContext(ctx)
		unavailable := func() { writeError(w, http.StatusServiceUnavailable, "internal", "概览服务未初始化") }
		fail := func(err error) {
			if !writeAuthorizationError(w, err) {
				writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			}
		}
		switch chi.URLParam(r, "section") {
		case "recent", "running":
			if o.runs == nil {
				unavailable()
				return
			}
			filter := run.ListFilter{Page: 1, PageSize: 12}
			if chi.URLParam(r, "section") == "running" {
				filter.Status = run.StatusRunning
				filter.PageSize = 1
			}
			result, err := o.runs.List(ctx, filter)
			if err != nil {
				fail(err)
				return
			}
			items := make([]runListItemDTO, 0, len(result.Items))
			for i := range result.Items {
				items = append(items, toRunListItemDTO(&result.Items[i]))
			}
			writeJSON(w, 200, runListDTO{Items: items, Total: result.Total, Page: result.Page})
		case "projects":
			if o.projects == nil {
				unavailable()
				return
			}
			projects, err := o.projects.List(ctx)
			if err != nil {
				fail(err)
				return
			}
			items := make([]run.MetricsProject, 0, len(projects))
			for _, p := range projects {
				items = append(items, run.MetricsProject{ID: p.ID, Name: p.Name})
			}
			writeJSON(w, 200, items)
		case "servers":
			if o.servers == nil {
				unavailable()
				return
			}
			servers, err := o.servers.List(ctx)
			if err != nil {
				fail(err)
				return
			}
			type summary struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Host string `json:"host"`
			}
			items := make([]summary, 0, len(servers))
			for _, s := range servers {
				items = append(items, summary{s.ID, s.Name, s.Host})
			}
			writeJSON(w, 200, items)
		case "metrics":
			makeDashboardMetricsHandler(o)(w, r)
		case "dora":
			makeDoraMetricsHandler(o.doraMetrics)(w, r)
		case "diagnosis":
			if o.feedback == nil {
				unavailable()
				return
			}
			stats, err := o.feedback.GetStats(ctx)
			if err != nil {
				fail(err)
				return
			}
			// The overview needs counts, not people's correction text or run snapshots.
			writeJSON(w, 200, map[string]any{"totalFeedback": stats.TotalFeedback, "thumbsUp": stats.ThumbsUp, "thumbsDown": stats.ThumbsDown, "accuracy": stats.Accuracy})
		case "environments":
			if o.projects == nil || o.environments == nil {
				unavailable()
				return
			}
			projects, err := o.projects.List(ctx)
			if err != nil {
				fail(err)
				return
			}
			type active struct {
				Commit     string `json:"commit"`
				Branch     string `json:"branch"`
				DeployedAt string `json:"deployedAt"`
			}
			type entry struct {
				ProjectID   string  `json:"projectId"`
				ProjectName string  `json:"projectName"`
				Environment string  `json:"environment"`
				Active      *active `json:"active"`
			}
			items := []entry{}
			for _, p := range projects {
				timelines, err := o.environments.ListEnvironments(ctx, p.ID, 1)
				if err != nil {
					fail(err)
					return
				}
				for _, tl := range timelines {
					item := entry{ProjectID: p.ID, ProjectName: p.Name, Environment: tl.Environment}
					if tl.Active != nil {
						item.Active = &active{tl.Active.Commit, tl.Active.Branch, tl.Active.DeployedAt}
					}
					items = append(items, item)
				}
			}
			sort.SliceStable(items, func(i, j int) bool {
				if items[i].Active == nil {
					return false
				}
				if items[j].Active == nil {
					return true
				}
				return items[i].Active.DeployedAt > items[j].Active.DeployedAt
			})
			if len(items) > 8 {
				items = items[:8]
			}
			writeJSON(w, 200, items)
		case "alerts":
			if o.anomaly == nil {
				unavailable()
				return
			}
			alerts, err := o.anomaly.ListAlerts(ctx, "", 6)
			if err != nil {
				fail(err)
				return
			}
			type summary struct {
				ID         string `json:"id"`
				Metric     string `json:"metric"`
				Message    string `json:"message"`
				ServerName string `json:"serverName"`
				At         string `json:"at"`
			}
			items := make([]summary, 0, len(alerts))
			for _, a := range alerts {
				dto := toAlertDTO(a)
				items = append(items, summary{dto.ID, dto.Metric, dto.Message, dto.ServerName, dto.At})
			}
			writeJSON(w, 200, items)
		default:
			writeError(w, http.StatusNotFound, "not_found", "页面不存在")
		}
	}
}

// Missing execution permission is an unknown sample, not a network outage.
func makeDashboardMetricsHandler(o *options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if o.servers == nil {
			writeError(w, 503, "internal", "服务器服务未初始化")
			return
		}
		enabled, err := tenancy.Enabled(r.Context(), o.configDB)
		if err != nil {
			accessError(w, err)
			return
		}
		if !enabled {
			makeAllServerMetricsHandler(o.servers)(w, r)
			return
		}
		servers, err := o.servers.List(r.Context())
		if err != nil {
			accessError(w, err)
			return
		}
		samples := make([]*serverMetricsDTO, len(servers))
		sem := make(chan struct{}, metricsConcurrency)
		var wg sync.WaitGroup
		for i, s := range servers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-r.Context().Done():
					return
				}
				defer func() { <-sem }()
				allowed := func() bool {
					return tenancy.Check(r.Context(), o.configDB, "server.use", "server", s.ID) == nil && tenancy.Check(r.Context(), o.configDB, "credential.use", "credential", s.CredentialID) == nil
				}
				if !allowed() {
					return
				}
				sample, _ := collectServerMetrics(r.Context(), o.servers, s.ID)
				if !allowed() {
					return
				}
				samples[i] = &sample
			}()
		}
		wg.Wait()
		if _, err := tenancy.WithDashboardScope(r.Context(), o.configDB); err != nil {
			accessError(w, err)
			return
		}
		items := []serverMetricsDTO{}
		for _, sample := range samples {
			if sample != nil {
				items = append(items, *sample)
			}
		}
		writeJSON(w, 200, map[string]any{"items": items})
	}
}
