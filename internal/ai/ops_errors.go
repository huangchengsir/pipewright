package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/huangchengsir/pipewright/internal/opschat"
)

// OpsFailure carries only allowlisted diagnostics, never provider text or URLs.
// The worker consumes its structural OpsFailureCode interface without importing AI.
type OpsFailure struct {
	Code       string
	HTTPStatus int
}

func (e *OpsFailure) SafeCode() string {
	if e != nil {
		switch e.Code {
		case "http_auth", "rate_limit", "context_limit", "timeout", "cancelled", "rejected", "invalid_response", "token_limit", "empty_response", "incomplete_stream", "transport_error", "response_limit":
			return e.Code
		}
	}
	return "invalid_response"
}

func (e *OpsFailure) OpsFailureCode() string {
	switch e.SafeCode() {
	case "token_limit":
		return "token_limit"
	case "timeout":
		return "timeout"
	case "http_auth", "rate_limit", "context_limit", "rejected":
		return "rejected"
	case "transport_error", "cancelled":
		return "unavailable"
	}
	return "invalid_response"
}

func (e *OpsFailure) Error() string {
	code := e.SafeCode()
	if e != nil && e.HTTPStatus >= 100 && e.HTTPStatus <= 599 {
		return fmt.Sprintf("ai: ops model %s (HTTP %d)", code, e.HTTPStatus)
	}
	return "ai: ops model " + code
}

func (e *OpsFailure) Unwrap() error {
	switch e.SafeCode() {
	case "timeout":
		return context.DeadlineExceeded
	case "cancelled":
		return context.Canceled
	}
	return nil
}

func opsFailure(code string, status int) error {
	return fmt.Errorf("%w: %w", ErrGenerateFailed, &OpsFailure{Code: code, HTTPStatus: status})
}

func opsHTTPFailure(status int) error {
	code := "rejected"
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = "http_auth"
	case http.StatusTooManyRequests:
		code = "rate_limit"
	case http.StatusRequestEntityTooLarge:
		code = "context_limit"
	}
	return opsFailure(code, status)
}

func opsTransportFailure(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return opsFailure("cancelled", 0)
	}
	var timed net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timed) && timed.Timeout() {
		return opsFailure("timeout", 0)
	}
	return opsFailure("transport_error", 0)
}

func opsResponseLimit() error {
	return fmt.Errorf("%w: %w", opschat.ErrQuota, opsFailure("response_limit", 0))
}

func opsStopFailure(reason string, accepted ...string) error {
	if reason == "length" || reason == "max_tokens" || reason == "limit" {
		return opsFailure("token_limit", 0)
	}
	for _, value := range accepted {
		if reason == value {
			return nil
		}
	}
	return opsFailure("rejected", 0)
}

// Inspect completion metadata only; never retain or return provider error bodies.
func opsCompletionFailure(provider string, raw []byte) error {
	var response struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Index        int     `json:"index"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		StopReason *string `json:"stop_reason"`
		DoneReason *string `json:"done_reason"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return opsFailure("invalid_response", 0)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return opsFailure("rejected", 0)
	}
	switch provider {
	case ProviderOpenAI:
		for _, choice := range response.Choices {
			if choice.Index == 0 && choice.FinishReason != nil {
				return opsStopFailure(*choice.FinishReason, "stop")
			}
		}
	case ProviderClaude:
		if response.StopReason != nil {
			return opsStopFailure(*response.StopReason, "end_turn", "stop_sequence")
		}
	case ProviderOllama:
		if response.DoneReason != nil {
			return opsStopFailure(*response.DoneReason, "", "stop")
		}
	}
	return nil
}
