package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/deploy"
)

func batchService(w http.ResponseWriter, svc deploy.Service) deploy.BatchService {
	batch, ok := svc.(deploy.BatchService)
	if !ok || svc == nil {
		writeError(w, http.StatusServiceUnavailable, "internal", "批量部署服务未初始化")
		return nil
	}
	return batch
}

func makeCreateDeployBatchHandler(svc deploy.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		batch := batchService(w, svc)
		if batch == nil {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var input deploy.BatchInput
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		input.RunID = chi.URLParam(r, "id")
		result, err := batch.DeployBatch(r.Context(), input)
		if err != nil {
			writeDeployError(w, err)
			return
		}
		recordBatchAudit(r, aud, audit.ActionDeployBatchCreate, result)
		writeJSON(w, http.StatusOK, result)
	}
}

func makeListDeployBatchesHandler(svc deploy.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		batch := batchService(w, svc)
		if batch == nil {
			return
		}
		results, err := batch.ListBatches(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeDeployError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"batches": results})
	}
}

func makeGetDeployBatchHandler(svc deploy.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		batch := batchService(w, svc)
		if batch == nil {
			return
		}
		result, err := batch.GetBatch(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "batchId"))
		if err != nil {
			writeDeployError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func makeRetryDeployBatchHandler(svc deploy.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		batch := batchService(w, svc)
		if batch == nil {
			return
		}
		result, err := batch.RetryBatch(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "batchId"))
		if err != nil {
			writeDeployError(w, err)
			return
		}
		recordBatchAudit(r, aud, audit.ActionDeployBatchRetry, result)
		writeJSON(w, http.StatusOK, result)
	}
}

func makeContinueDeployBatchHandler(svc deploy.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		batch := batchService(w, svc)
		if batch == nil {
			return
		}
		result, err := batch.ContinueBatch(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "batchId"))
		if err != nil {
			writeDeployError(w, err)
			return
		}
		recordBatchAudit(r, aud, audit.ActionDeployBatchContinue, result)
		writeJSON(w, http.StatusOK, result)
	}
}

func recordBatchAudit(r *http.Request, rec audit.Recorder, action string, result *deploy.BatchResult) {
	recordAudit(r.Context(), rec, audit.Entry{
		Actor: auditActor, Action: action, TargetType: "deploy_batch", TargetID: result.ID,
		Detail: map[string]any{"runId": result.RunID, "status": result.Status}, IP: clientIP(r),
	})
}
