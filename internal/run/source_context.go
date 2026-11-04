package run

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
)

// SourceContext is an immutable snapshot of the actual source bound to a run.
type SourceContext struct {
	RefName, CommitSHA                   string
	CommitTitle, CommitAuthor, SourceURL string
	MetadataReady                        bool
}
type sourceState struct {
	mu     sync.RWMutex
	source SourceContext
}
type sourceKey struct{}

var sourceSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var ErrSourceUnavailable = errors.New("run: source context unavailable or conflicting")

// WithSourceContext installs a run-scoped source binding; never share between runs.
func WithSourceContext(ctx context.Context, initial SourceContext) context.Context {
	return context.WithValue(ctx, sourceKey{}, &sourceState{source: initial})
}

// WithoutSourceContext masks an inherited binding when a graph uses compatibility mode.
// Other run-scoped values, including recovery and preparation, remain available.
func WithoutSourceContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, sourceKey{}, struct{}{})
}

func SourceFromContext(ctx context.Context) (SourceContext, bool) {
	state, ok := ctx.Value(sourceKey{}).(*sourceState)
	if !ok {
		return SourceContext{}, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.source, true
}

// BindSourceContext publishes a confirmed SHA once. Conflicting values fail closed.
func BindSourceContext(ctx context.Context, source SourceContext) error {
	state, ok := ctx.Value(sourceKey{}).(*sourceState)
	source.CommitSHA = strings.ToLower(strings.TrimSpace(source.CommitSHA))
	source.RefName = strings.TrimSpace(source.RefName)
	if !ok || !sourceSHA.MatchString(source.CommitSHA) {
		return ErrSourceUnavailable
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.source.CommitSHA != "" && state.source.CommitSHA != source.CommitSHA {
		return ErrSourceUnavailable
	}
	if state.source.RefName != "" && source.RefName != "" && state.source.RefName != source.RefName {
		return ErrSourceUnavailable
	}
	if source.RefName == "" {
		source.RefName = state.source.RefName
	}
	if state.source.MetadataReady {
		if source.MetadataReady && (source.CommitTitle != state.source.CommitTitle || source.CommitAuthor != state.source.CommitAuthor || source.SourceURL != state.source.SourceURL) {
			return ErrSourceUnavailable
		}
		if !source.MetadataReady {
			source.CommitTitle = state.source.CommitTitle
			source.CommitAuthor = state.source.CommitAuthor
			source.SourceURL = state.source.SourceURL
			source.MetadataReady = true
		}
	}
	state.source = source
	return nil
}
