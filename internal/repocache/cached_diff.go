package repocache

import (
	"context"
	"errors"
	"os"
	"regexp"
	"sort"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
)

var cachedCommitSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var errCachedBudget = errors.New("cached comparison budget exceeded")
var errCachedPath = errors.New("cached comparison path unsupported")
var errCachedTruncated = errors.New("cached comparison truncated")

// CompareLocal only reads this Cache's existing, hashed mirror directory. It
// never calls ensureMirror, fallback, fetch, clone, a shell, or blob readers.
// A busy build retains priority; callers get an explicit unavailable result.
func (c *Cache) CompareLocal(ctx context.Context, repoURL, base, current string) releaseguard.SourceComparison {
	out := releaseguard.SourceComparison{BaseSHA: base, CurrentSHA: current, ComparisonKind: "direct_trees", Files: []releaseguard.SourceFileChange{}}
	unavailable := func(reason string) releaseguard.SourceComparison {
		out.Available = false
		out.Reason = reason
		out.Files = []releaseguard.SourceFileChange{}
		return out
	}
	if !cachedCommitSHA.MatchString(base) || !cachedCommitSHA.MatchString(current) {
		out.BaseSHA = ""
		out.CurrentSHA = ""
		return unavailable("full_commit_required")
	}
	if c == nil || repoURL == "" {
		return unavailable("source_cache_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return unavailable("source_comparison_cancelled")
	}
	lock := c.repoLock(repoURL)
	if !lock.TryLock() {
		return unavailable("source_cache_busy")
	}
	defer lock.Unlock()
	mirror := c.mirrorPath(repoURL)
	info, err := os.Lstat(mirror)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return unavailable("source_cache_missing")
	}
	repo, err := gogit.PlainOpen(mirror)
	if err != nil {
		return unavailable("source_cache_unreadable")
	}
	baseCommit, err := cachedCommit(repo, plumbing.NewHash(base))
	if err != nil {
		return unavailable("base_commit_not_cached")
	}
	currentCommit, err := cachedCommit(repo, plumbing.NewHash(current))
	if err != nil {
		return unavailable("current_commit_not_cached")
	}
	if baseCommit.Hash.String() != base || currentCommit.Hash.String() != current {
		return unavailable("cached_commit_identity_invalid")
	}
	type entry struct {
		hash plumbing.Hash
		mode filemode.FileMode
	}
	before := map[string]entry{}
	budget := cachedTreeBudget{entries: 5000, bytes: 1 << 20}
	err = walkCachedTree(ctx, repo, baseCommit.TreeHash, "", 0, &budget, func(name string, e object.TreeEntry) error { before[name] = entry{e.Hash, e.Mode}; return nil })
	if err != nil {
		return unavailable(cachedFailureReason(err))
	}
	changes := []releaseguard.SourceFileChange{}
	add := func(name, status string) error {
		if len(changes) == 40 {
			return errCachedTruncated
		}
		changes = append(changes, releaseguard.SourceFileChange{Path: name, Status: status})
		return nil
	}
	err = walkCachedTree(ctx, repo, currentCommit.TreeHash, "", 0, &budget, func(name string, e object.TreeEntry) error {
		prior, exists := before[name]
		delete(before, name)
		if !exists {
			return add(name, "added")
		}
		if prior.hash != e.Hash || prior.mode != e.Mode {
			return add(name, "modified")
		}
		return nil
	})
	if err == nil {
		deleted := make([]string, 0, len(before))
		for name := range before {
			deleted = append(deleted, name)
		}
		sort.Strings(deleted)
		for _, name := range deleted {
			if ctx.Err() != nil {
				err = ctx.Err()
				break
			}
			if err = add(name, "deleted"); err != nil {
				break
			}
		}
	}
	if err != nil && !errors.Is(err, errCachedTruncated) {
		return unavailable(cachedFailureReason(err))
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	out.Available = true
	out.Files = changes
	out.Truncated = errors.Is(err, errCachedTruncated)
	if out.Truncated {
		out.Reason = "file_limit"
	}
	return out
}

type cachedTreeBudget struct {
	entries int
	bytes   int64
}

func cachedCommit(repo *gogit.Repository, hash plumbing.Hash) (*object.Commit, error) {
	encoded, err := repo.Storer.EncodedObject(plumbing.CommitObject, hash)
	if err != nil {
		return nil, err
	}
	if encoded.Size() < 0 || encoded.Size() > 64<<10 {
		return nil, errCachedBudget
	}
	return object.DecodeCommit(repo.Storer, encoded)
}

// Decode bounded tree objects explicitly. go-git's TreeWalker can silently
// skip an unavailable subtree; that would make an incomplete diff look whole.
func walkCachedTree(ctx context.Context, repo *gogit.Repository, hash plumbing.Hash, prefix string, depth int, budget *cachedTreeBudget, visit func(string, object.TreeEntry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 64 {
		return errCachedBudget
	}
	encoded, err := repo.Storer.EncodedObject(plumbing.TreeObject, hash)
	if err != nil {
		return err
	}
	if encoded.Size() < 0 || encoded.Size() > 256<<10 || encoded.Size() > budget.bytes {
		return errCachedBudget
	}
	budget.bytes -= encoded.Size()
	tree, err := object.DecodeTree(repo.Storer, encoded)
	if err != nil {
		return err
	}
	for _, e := range tree.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		budget.entries--
		if budget.entries < 0 {
			return errCachedBudget
		}
		name := e.Name
		if prefix != "" {
			name = prefix + "/" + name
		}
		if !releaseguard.ValidSourceChangePath(name) {
			return errCachedPath
		}
		if e.Mode == filemode.Dir {
			if err := walkCachedTree(ctx, repo, e.Hash, name, depth+1, budget, visit); err != nil {
				return err
			}
		} else {
			if err := visit(name, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func cachedFailureReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "source_comparison_cancelled"
	case errors.Is(err, errCachedBudget):
		return "source_tree_budget_exceeded"
	case errors.Is(err, errCachedPath):
		return "source_path_unsupported"
	default:
		return "source_tree_unavailable"
	}
}
