package build

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

type sourceResolveKey struct{}

// resolveRunSource confirms a checkout before publishing the immutable run source.
func (b *Builder) resolveRunSource(ctx context.Context, r *run.Run) error {
	proj, _, err := b.resolve(ctx, r)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(r.Trigger.Branch)
	if ref == "" {
		ref = strings.TrimSpace(proj.DefaultBranch)
	}
	ws, err := mkTempWorkspace()
	if err != nil {
		return err
	}
	defer os.RemoveAll(ws)
	copyRun := *r
	copyRun.Trigger.Branch = ref
	resolving := context.WithValue(ctx, sourceResolveKey{}, true)
	resolved, err := b.cloneWithGitAuth(resolving, proj, &copyRun, ws)
	if err != nil {
		return err
	}
	if resolved == nil || len(resolved.CommitFull) != 40 {
		return run.ErrSourceUnavailable
	}
	if _, err := hex.DecodeString(resolved.CommitFull); err != nil {
		return run.ErrSourceUnavailable
	}
	if b.bindSourceCommit == nil {
		return errors.New("source commit persistence not configured")
	}
	if err := b.bindSourceCommit(ctx, r.ID, resolved.CommitFull); err != nil {
		return fmt.Errorf("persist source commit: %w", err)
	}
	return bindCheckoutSource(ctx, ref, proj.RepoURL, resolved)
}

// safeSourceURL never publishes embedded authentication or query/fragment tokens.
func safeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return ""
		}
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		return u.String()
	}
	// Git scp-style SSH URLs: remove the user component, then any query/fragment.
	if at := strings.IndexByte(raw, '@'); at >= 0 {
		raw = raw[at+1:]
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func bindCheckoutSource(ctx context.Context, ref, repoURL string, resolved *CloneResolved) error {
	if resolved == nil {
		return run.ErrSourceUnavailable
	}
	next := run.SourceContext{RefName: ref, CommitSHA: resolved.CommitFull}
	if !resolved.MetadataReady {
		return run.ErrSourceUnavailable
	}
	next.CommitTitle = resolved.CommitTitle
	next.CommitAuthor = resolved.CommitAuthor
	next.SourceURL = safeSourceURL(repoURL)
	next.MetadataReady = true
	return run.BindSourceContext(ctx, next)
}

func appendSourceEnv(ctx context.Context, r *run.Run, vars []pipeline.BuildVar) []pipeline.BuildVar {
	source, active := run.SourceFromContext(ctx)
	if !active || source.CommitSHA == "" {
		return vars
	}
	vars = append(vars,
		pipeline.BuildVar{Key: "CI_COMMIT_SHA", Value: source.CommitSHA},
		pipeline.BuildVar{Key: "CI_COMMIT_ID", Value: shortSHA(source.CommitSHA)},
		pipeline.BuildVar{Key: "CI_COMMIT_REF_NAME", Value: source.RefName},
	)
	if !source.MetadataReady || r == nil {
		return vars
	}
	datetime, timestamp := "", ""
	if !r.CreatedAt.IsZero() {
		t := r.CreatedAt.UTC()
		datetime = t.Format("2006-01-02 15:04:05")
		timestamp = strconv.FormatInt(t.Unix(), 10)
	}
	return append(vars,
		pipeline.BuildVar{Key: "PIPELINE_ID", Value: r.ID},
		pipeline.BuildVar{Key: "DATETIME", Value: datetime},
		pipeline.BuildVar{Key: "TIMESTAMP", Value: timestamp},
		pipeline.BuildVar{Key: "CI_COMMIT_TITLE", Value: source.CommitTitle},
		pipeline.BuildVar{Key: "CI_COMMIT_AUTHOR", Value: source.CommitAuthor},
		pipeline.BuildVar{Key: "CI_COMMIT_SOURCE", Value: source.SourceURL},
	)
}
