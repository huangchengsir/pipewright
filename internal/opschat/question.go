package opschat

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func shortConfirmation(text string) bool {
	text = strings.TrimRight(strings.ToLower(strings.TrimSpace(text)), ".!。！")
	switch text {
	case "确认", "确认执行", "可以", "好的", "好", "confirm", "confirm execution", "yes", "ok", "okay":
		return true
	}
	return false
}

// Bind only an unfinished conversational request, never an approval or a replay.
func (s *Service) turnQuestion(ctx context.Context, tx *sql.Tx, id string, body runBody) (string, error) {
	question := body.Turn.Text
	if !shortConfirmation(question) {
		return question, nil
	}
	var previousID, kind string
	err := tx.QueryRowContext(ctx, `SELECT run_id,kind FROM ops_chat_entries
		WHERE session_id=? AND kind IN ('user','tool_request') ORDER BY seq DESC LIMIT 1`, id).Scan(&previousID, &kind)
	if err == sql.ErrNoRows {
		return question, nil
	}
	if err != nil {
		return "", ErrStorage
	}
	if kind != "user" || previousID == "" {
		return question, nil
	}
	var latestRun string
	if err := tx.QueryRowContext(ctx, `SELECT run_id FROM ops_chat_entries
		WHERE session_id=? AND run_id<>'' ORDER BY seq DESC LIMIT 1`, id).Scan(&latestRun); err != nil {
		return "", ErrStorage
	}
	if latestRun != previousID {
		return question, nil
	}
	previous, err := s.run(ctx, tx, id, previousID)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if previous.View.Status != Succeeded || previous.View.CancelRequested || previous.View.CreatedAt.Before(now.Add(-15*time.Minute)) || previous.View.CreatedAt.After(now) || previous.Body.Provider != body.Provider || previous.Body.Turn.ToolID != "" || previous.Body.Analysis != nil || previous.Body.AnalysisStarted || len(previous.Body.RetryActions) != 0 || len(previous.Body.Targets) != len(body.Targets) {
		return question, nil
	}
	for i, snapshot := range body.Targets {
		if snapshot.Server.ID != previous.Body.Targets[i].Server.ID || snapshot.Fingerprint() != previous.Body.Targets[i].Fingerprint() {
			return question, nil
		}
	}
	calls, err := s.calls(ctx, tx, id, previousID)
	if err != nil {
		return "", err
	}
	for _, call := range calls {
		if call.Body.View.ToolID != "docker_containers" || call.Body.View.Status != Succeeded {
			return question, nil
		}
	}
	original := previous.Body.Question
	if original == "" {
		original = previous.Body.Turn.Text
	}
	if strings.TrimSpace(original) == "" || shortConfirmation(original) {
		return question, nil
	}
	return s.scrub(original, 8<<10), nil
}
