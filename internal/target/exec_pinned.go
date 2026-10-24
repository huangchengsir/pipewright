package target

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// PinnedExecutor binds deployment IO to a server-created connection snapshot.
// It adds no HTTP permission or command execution endpoint. Unlike observation
// reads, a build may use the caller's longer deadline; output stays bounded.
type PinnedExecutor interface {
	LimitedExecutor
	ExecPinned(context.Context, ConnectionSnapshot, []string, ExecutionLimits) (*LimitedResult, error)
	UploadPinned(context.Context, ConnectionSnapshot, io.Reader, string) error
}

type limitedStdinRunner interface {
	RunLimitedWithStdin(context.Context, string, SSHConfig, []string, io.Reader, ExecutionLimits) (*LimitedResult, error)
}

func (s *service) ExecPinned(ctx context.Context, approved ConnectionSnapshot, cmd []string, limits ExecutionLimits) (*LimitedResult, error) {
	return s.execApproved(ctx, approved, cmd, limits, 30*time.Minute, 256, 32<<10)
}

func (s *service) UploadPinned(ctx context.Context, approved ConnectionSnapshot, content io.Reader, remotePath string) error {
	if content == nil || strings.TrimSpace(remotePath) == "" || len(remotePath) > 4096 || strings.ContainsAny(remotePath, "\x00\r\n") {
		return errors.New("target: invalid upload")
	}
	runner, ok := s.dialer.(limitedStdinRunner)
	if !ok {
		return ErrLimitedUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return s.withApprovedSSH(ctx, approved, func(addr string, cfg SSHConfig) error {
		cmd := []string{"sh", "-c", `mkdir -p "$(dirname "$0")" && cat > "$0"`, remotePath}
		result, err := runner.RunLimitedWithStdin(ctx, addr, cfg, cmd, content, ExecutionLimits{Bytes: 4096, Lines: 40})
		if err != nil {
			return err
		}
		if result == nil || result.ExitCode != 0 {
			// Remote stderr may contain script data; never expose it in errors.
			return errors.New("target: pinned upload failed")
		}
		return nil
	})
}
