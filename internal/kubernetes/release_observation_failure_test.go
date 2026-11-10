package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type observationFailureAPI struct {
	releaseAPI
	read func(string, string) ([]byte, error)
}

func (a observationFailureAPI) Read(_ context.Context, p, q string) ([]byte, error) {
	return a.read(p, q)
}
func TestReleaseObservationFailureRecordsClosedStageWithoutPrivateError(t *testing.T) {
	for _, tc := range []struct {
		name, stage, reason string
		cause               error
	}{
		{"first_read", "resource_read", "kubernetes_tool_unavailable", ErrTool},
		{"list_read", "related_list_read", "kubernetes_private_output_limit", ErrPrivateLimit},
		{"list_identity", "related_list_decode", "kubernetes_invalid_input", ErrInvalid},
		{"anchor_read", "related_anchor_read", "kubernetes_not_found", ErrNotFound},
		{"anchor_identity", "related_filter", "kubernetes_not_authorized", ErrDenied},
		{"failed_health", "health_evaluation", "kubernetes_workload_not_ready", ErrRollout},
		{"no_pod_yet", "observation_wait", "observation_canceled", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "probe"}
			actual := `{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"probe","namespace":"app","uid":"owned-job"},"spec":{}}`
			declared := releaseTestObject(actual)
			delete(releaseMap(declared["metadata"]), "uid")
			resource := ReleaseResource{ID: id, Object: declared, Health: "workload"}
			calls := 0
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			var logs []string
			ctx := context.WithValue(base, releaseHealthLogKey{}, PhaseLog(func(p, c string) { logs = append(logs, p+": "+c) }))
			api := observationFailureAPI{read: func(path, query string) ([]byte, error) {
				calls++
				switch {
				case tc.name == "first_read":
					return nil, errors.Join(tc.cause, errors.New("PRIVATE_ERROR_CANARY"))
				case strings.HasSuffix(path, "/pods"):
					if tc.name == "list_read" {
						return nil, errors.Join(tc.cause, errors.New("PRIVATE_ERROR_CANARY"))
					}
					if tc.name == "list_identity" {
						return []byte(`{"apiVersion":"v1","kind":"SecretList","items":[]}`), nil
					}
					return []byte(`{"apiVersion":"v1","kind":"PodList","items":[]}`), nil
				case calls == 3 && tc.name == "anchor_read":
					return nil, tc.cause
				case calls == 3 && tc.name == "anchor_identity":
					return []byte(strings.Replace(actual, `"name":"probe"`, `"name":"foreign"`, 1)), nil
				case tc.name == "failed_health":
					return []byte(strings.TrimSuffix(actual, "}") + `,"status":{"conditions":[{"type":"Failed","status":"True"}]}}`), nil
				default:
					if calls == 3 && tc.name == "no_pod_yet" {
						cancel()
					}
					return []byte(actual), nil
				}
			}}
			receipt, err := NewReleaseController(nil, nil).observeResource(ctx, api, resource, 0)
			if !errors.Is(err, tc.cause) {
				t.Fatalf("cause changed: %v", err)
			}
			want := "observe: stage=" + tc.stage + " reason=" + tc.reason
			if len(logs) != 1 || logs[0] != want {
				t.Fatalf("missing closed first observation error: got %v want %s", logs, want)
			}
			if strings.Contains(strings.Join(logs, "\n"), "PRIVATE_ERROR_CANARY") {
				t.Fatal("private error leaked")
			}
			if tc.name == "no_pod_yet" && (receipt.Health.State != "pending" || receipt.Health.Reason != "job_not_completed") {
				t.Fatal("ordinary missing Pod ceased to be pending")
			}
			if tc.name == "anchor_identity" && calls != 3 {
				t.Fatal("anchor identity bypassed actual read/list/read path")
			}
		})
	}
}
