package build

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

func safeRecoveryDestination(root, relative string) (string, error) {
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || strings.ContainsAny(relative, "\\:\x00") || relative == ".." || strings.HasPrefix(relative, "../") || relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return "", runretry.ErrInvalid
	}
	dest := filepath.Join(root, filepath.FromSlash(relative))
	rel, e := filepath.Rel(root, dest)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", runretry.ErrInvalid
	}
	current := root
	for _, part := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		st, e := os.Lstat(current)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || st.Mode()&os.ModeSymlink != 0 {
			return "", runretry.ErrInvalid
		}
	}
	return dest, nil
}
func (b *Builder) restoreRecoveryFiles(ctx context.Context, ws string, files []runretry.FileRef) error {
	if len(files) == 0 {
		return nil
	}
	if b.artStore == nil {
		return runretry.ErrUnavailable
	}
	var total int64
	for _, f := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dest, err := safeRecoveryDestination(ws, f.Path)
		if err != nil {
			return err
		}
		if f.Size < 0 || f.Size > 512<<20 || total+f.Size > 1<<30 || f.Key != f.SHA256 {
			return runretry.ErrUnavailable
		}
		total += f.Size
		rc, err := b.artStore.Open(f.Key)
		if err != nil {
			return runretry.ErrUnavailable
		}
		temp, err := os.CreateTemp(ws, ".pipewright-recovery-input-")
		if err != nil {
			_ = rc.Close()
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(temp, h), io.LimitReader(&recoveryContextReader{ctx, rc}, f.Size+1))
		closeErr := rc.Close()
		valid := copyErr == nil && closeErr == nil && n == f.Size && hex.EncodeToString(h.Sum(nil)) == f.SHA256
		if valid {
			_, err = temp.Seek(0, 0)
			if err == nil {
				if f.Kind == "tar.gz" {
					err = extractRecoveryArchive(ctx, temp, ws, f.Path)
				} else {
					err = writeFileTo(temp, dest)
				}
			}
		} else {
			err = runretry.ErrUnavailable
		}
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		if err != nil {
			return err
		}
	}
	return nil
}

type recoveryContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *recoveryContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func extractRecoveryArchive(ctx context.Context, reader io.Reader, ws, relative string) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return runretry.ErrUnavailable
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var expanded int64
	for count := 0; ; count++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil || count >= 10000 {
			return runretry.ErrUnavailable
		}
		if h.Name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00") || name == ".." || strings.HasPrefix(name, "../") {
			return runretry.ErrInvalid
		}
		dest, err := safeRecoveryDestination(ws, path.Join(relative, name))
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(dest, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || h.Size > 512<<20 || expanded+h.Size > 512<<20 {
				return runretry.ErrUnavailable
			}
			expanded += h.Size
			if err = writeFileTo(&recoveryContextReader{ctx, io.LimitReader(tr, h.Size)}, dest); err != nil {
				return err
			}
		default:
			return runretry.ErrInvalid // links, devices, sparse files are never restored
		}
	}
}

// The scan is bounded and never retains file contents. Unreadable, symbolic,
// huge or high-cardinality workspaces are conservatively incomplete; caches
// are not silently skipped to manufacture a complete checkpoint.
type recoveryWorkspaceScan struct {
	files    map[string]string
	complete bool
}

func scanRecoveryWorkspace(ctx context.Context, ws string) recoveryWorkspaceScan {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := recoveryWorkspaceScan{files: map[string]string{}, complete: true}
	deadline := time.Now().Add(2 * time.Second)
	var bytes int64
	err := filepath.WalkDir(ws, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) || len(result.files) >= 4096 {
			return runretry.ErrUnavailable
		}
		rel, e := filepath.Rel(ws, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		st, e := d.Info()
		if e != nil || !st.Mode().IsRegular() {
			return runretry.ErrUnavailable
		}
		bytes += st.Size()
		if bytes > 64<<20 {
			return runretry.ErrUnavailable
		}
		file, e := os.Open(p)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(&recoveryContextReader{ctx, file}, st.Size()+1))
		ce := file.Close()
		if e != nil || ce != nil || n != st.Size() {
			return runretry.ErrUnavailable
		}
		result.files[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		result.complete = false
	}
	return result
}
func workspaceIncomplete(ctx context.Context, ws string, before recoveryWorkspaceScan, rep dagrun.StageReporter) bool {
	after := scanRecoveryWorkspace(ctx, ws)
	if !before.complete || !after.complete {
		return true
	}
	recorder, ok := rep.(*recoveryReporter)
	if !ok {
		return true
	}
	covered := func(name string) bool {
		for _, f := range recorder.outputs.Files {
			if name == f.Path || f.Kind == "tar.gz" && strings.HasPrefix(name, f.Path+"/") {
				return true
			}
		}
		return false
	}
	for p, h := range after.files {
		if old, exists := before.files[p]; (!exists || old != h) && !covered(p) {
			return true
		}
	}
	for p := range before.files {
		if _, exists := after.files[p]; !exists && !covered(p) {
			return true
		}
	}
	return false
}
