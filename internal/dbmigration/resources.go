package dbmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

var ErrResources = errors.New("dbmigration: original key or referenced local resource verification failed")

// ResolvePaths pins the original defaults before a database switch. The caller
// supplies the existing environment overrides, not values derived from the target.
func ResolvePaths(data, dbPath, artifacts, repositories, cache string) (RuntimePaths, error) {
	values := []string{data, artifacts, repositories, cache}
	defaults := []string{data, filepath.Join(filepath.Dir(dbPath), "artifacts"), filepath.Join(filepath.Dir(dbPath), "repos"), filepath.Join(filepath.Dir(dbPath), "cache")}
	for i := range values {
		if strings.TrimSpace(values[i]) == "" {
			values[i] = defaults[i]
		}
		abs, e := filepath.Abs(values[i])
		if e != nil {
			return RuntimePaths{}, ErrResources
		}
		// Existing roots are canonicalized; missing optional caches stay absolute.
		if resolved, e := filepath.EvalSymlinks(abs); e == nil {
			abs = resolved
		} else if !errors.Is(e, os.ErrNotExist) {
			return RuntimePaths{}, ErrResources
		}
		values[i] = filepath.Clean(abs)
	}
	return RuntimePaths{DataDir: values[0], Artifacts: values[1], Repositories: values[2], Cache: values[3]}, nil
}

// VerifySecrets authenticates every non-null encrypted column in the reviewed
// manifest. Current binary business columns all use vault.SealSecret; adding a
// raw binary column requires an explicit manifest/verification contract change.
// Plaintext remains local, is cleared immediately and is never returned/logged.
func VerifySecrets(ctx context.Context, db *store.Store, key *[32]byte) error {
	if key == nil {
		return ErrResources
	}
	v := vault.New(db.DB, key)
	for _, table := range Manifest() {
		for _, col := range table.Columns {
			if col.Kind != Binary {
				continue
			}
			rows, e := db.DB.QueryContext(ctx, "SELECT "+store.Identifier(db.Dialect, col.Name)+" FROM "+store.Identifier(db.Dialect, table.Name)+" WHERE "+store.Identifier(db.Dialect, col.Name)+" IS NOT NULL")
			if e != nil {
				return ErrResources
			}
			for rows.Next() {
				var cipher []byte
				if rows.Scan(&cipher) != nil || len(cipher) > maxRowBytes {
					rows.Close()
					return ErrResources
				}
				plain, e := v.OpenSecret(cipher)
				clear(plain)
				if e != nil {
					rows.Close()
					return ErrResources
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return ErrResources
			}
		}
	}
	return nil
}

// VerifyArtifacts reads existing CAS objects; it never creates directories or
// downloads missing data. Logs already held in run_logs are verified by Copy.
func VerifyArtifacts(ctx context.Context, db *store.Store, paths RuntimePaths) error {
	rows, e := db.DB.QueryContext(ctx, "SELECT "+store.Identifier(db.Dialect, "reference")+",size_bytes,metadata_json FROM run_artifacts")
	if e != nil {
		return ErrResources
	}
	defer rows.Close()
	for rows.Next() {
		var ref, metadata string
		var size int64
		if rows.Scan(&ref, &size, &metadata) != nil {
			return ErrResources
		}
		var m struct {
			Stored bool `json:"stored"`
		}
		if metadata != "" && json.Unmarshal([]byte(metadata), &m) != nil {
			return ErrResources
		}
		if !m.Stored {
			continue
		}
		if e := verifyCAS(ctx, paths, ref, size); e != nil {
			return e
		}
	}
	if rows.Err() != nil {
		return ErrResources
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

// VerifyLocalResources includes files referenced only by private retry
// checkpoints; a run_artifacts row is not required for those retained objects.
func VerifyLocalResources(ctx context.Context, db *store.Store, key *[32]byte, paths RuntimePaths) error {
	if e := VerifyArtifacts(ctx, db, paths); e != nil {
		return e
	}
	if key == nil {
		return ErrResources
	}
	retries := runretry.New(db.DB, vault.New(db.DB, key))
	if e := retries.VerifyStoredFiles(ctx, func(ctx context.Context, hash string, size int64) error { return verifyCAS(ctx, paths, hash, size) }); e != nil {
		return ErrResources
	}
	return nil
}

func verifyCAS(ctx context.Context, paths RuntimePaths, ref string, size int64) error {
	digest, e := hex.DecodeString(ref)
	if e != nil || len(digest) != sha256.Size || ref != strings.ToLower(ref) || size < 0 {
		return ErrResources
	}
	path := filepath.Join(paths.Artifacts, ref[:2], ref)
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || filepath.Clean(path) != filepath.Clean(resolved) {
		return ErrResources
	}
	f, e := os.Open(path)
	if e != nil {
		return ErrResources
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != size {
		f.Close()
		return ErrResources
	}
	hash := sha256.New()
	n, e := io.Copy(hash, &contextReader{ctx: ctx, reader: io.LimitReader(f, size+1)})
	closeErr := f.Close()
	if e != nil || closeErr != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != ref {
		return ErrResources
	}
	return nil
}
