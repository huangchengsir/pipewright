package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/target"
	"io"
	"net/http"
)

type executionAssignmentDTO struct {
	TeamID     string `json:"teamId"`
	HostKey    string `json:"hostKey"`
	AssignedAt int64  `json:"assignedAt"`
}

func makeExecutionAssignmentHandler(service target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := service.(target.ExecutionAssignmentService)
		if !ok {
			writeError(w, 503, "unavailable", "Service unavailable")
			return
		}
		var value *target.ExecutionAssignment
		var err error
		if r.Method == http.MethodPut {
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			var input struct {
				HostKey *string `json:"hostKey"`
			}
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil || input.HostKey == nil || decoder.Decode(new(any)) != io.EOF {
				writeError(w, 400, "bad_request", "Invalid request")
				return
			}
			value, err = svc.AssignExecution(r.Context(), chi.URLParam(r, "id"), *input.HostKey)
		} else {
			value, err = svc.GetExecutionAssignment(r.Context(), chi.URLParam(r, "id"))
		}
		if errors.Is(err, target.ErrHostIdentity) {
			writeError(w, 400, "invalid_host_key", "Invalid SSH host fingerprint")
			return
		}
		if err != nil {
			writeServerError(w, err)
			return
		}
		var output *executionAssignmentDTO
		if value != nil {
			output = &executionAssignmentDTO{TeamID: value.TeamID, HostKey: value.HostKey, AssignedAt: value.AssignedAt}
		}
		writeJSON(w, 200, map[string]any{"assignment": output})
	}
}
