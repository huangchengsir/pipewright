package opschat

import (
	"context"
	"errors"
)

// Persist only a closed set of codes, never provider bodies or transport errors.
func analysisFailureText(reason error) string {
	code := "unknown"
	var classified interface{ OpsFailureCode() string }
	if errors.As(reason, &classified) {
		switch classified.OpsFailureCode() {
		case "token_limit", "timeout", "rejected", "invalid_response", "unavailable":
			code = classified.OpsFailureCode()
		}
	} else if errors.Is(reason, context.DeadlineExceeded) {
		code = "timeout"
	} else if errors.Is(reason, ErrQuota) {
		code = "input_limit"
	} else if errors.Is(reason, ErrUnavailable) {
		code = "unavailable"
	}
	return "[analysis:" + code + "]"
}
