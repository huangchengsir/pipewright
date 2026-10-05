package run

import (
	"context"
	"database/sql"
	"sync"
)

const (
	ExecutionPending       = "pending"
	ExecutionReal          = "real"
	ExecutionStub          = "stub"
	ExecutionMixed         = "mixed"
	ExecutionLegacyUnknown = "legacy_unknown"
)

// ExecutionRecorder is optional on StepSink and StageReporter. It records actual
// invocations, not runner selection, configuration intent, or step status.
type ExecutionRecorder interface{ RecordExecution(mode string) }

func RecordExecution(target any, mode string) {
	if recorder, ok := target.(ExecutionRecorder); ok {
		recorder.RecordExecution(mode)
	}
}

// ExecutionEvidence is a run-scoped, concurrency-safe collector. Its zero value is pending.
type ExecutionEvidence struct {
	mu         sync.Mutex
	real, stub bool
}

func (e *ExecutionEvidence) RecordExecution(mode string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch mode {
	case ExecutionReal:
		e.real = true
	case ExecutionStub:
		e.stub = true
	}
}

func (e *ExecutionEvidence) Mode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.real && e.stub:
		return ExecutionMixed
	case e.real:
		return ExecutionReal
	case e.stub:
		return ExecutionStub
	default:
		return ExecutionPending
	}
}

// LoadExecutionMode reads evidence without changing the existing run API contracts.
func LoadExecutionMode(ctx context.Context, db *sql.DB, runID string) (string, error) {
	var mode string
	err := db.QueryRowContext(ctx, `SELECT execution_mode FROM pipeline_runs WHERE id = ?`, runID).Scan(&mode)
	return mode, err
}
