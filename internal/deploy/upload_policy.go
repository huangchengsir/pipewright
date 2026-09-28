package deploy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/huangchengsir/pipewright/internal/target"
)

var ErrInvalidUploadPolicy = errors.New("deploy: invalid upload policy")

type uploadPolicy struct {
	command, connect, idle, total time.Duration
}

type uploadPolicyKey struct{}

func parseUploadPolicy(cfg map[string]string) (uploadPolicy, error) {
	p := uploadPolicy{command: 60 * time.Second, connect: 15 * time.Second, idle: 120 * time.Second}
	for _, field := range []struct {
		key      string
		dst      *time.Duration
		min, max int
	}{
		{"commandTimeoutSeconds", &p.command, 1, 86400},
		{"connectTimeoutSeconds", &p.connect, 1, 300},
		{"uploadIdleTimeoutSeconds", &p.idle, 1, 86400},
		{"uploadTimeoutSeconds", &p.total, 0, 604800},
	} {
		if raw := cfg[field.key]; raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < field.min || n > field.max {
				return p, fmt.Errorf("%w: %s (%d..%d)", ErrInvalidUploadPolicy, field.key, field.min, field.max)
			}
			*field.dst = time.Duration(n) * time.Second
		}
	}
	return p, nil
}

func withUploadPolicy(ctx context.Context, cfg map[string]string) (context.Context, error) {
	p, err := parseUploadPolicy(cfg)
	if err != nil {
		return ctx, err
	}
	ctx = context.WithValue(ctx, uploadPolicyKey{}, p)
	return target.WithTransferTimeouts(ctx, p.connect, 0), nil
}

func policyFrom(ctx context.Context) uploadPolicy {
	if p, ok := ctx.Value(uploadPolicyKey{}).(uploadPolicy); ok {
		return p
	}
	p, _ := parseUploadPolicy(nil)
	return p
}
