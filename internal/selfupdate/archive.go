package selfupdate

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"strings"
)

func (s *Service) extract(ctx context.Context, t *Task) (name, digest string, err error) {
	f, e := s.root.Open(t.ID + ".part")
	if e != nil {
		return "", "", e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(contextReader{ctx, f}, MaxArchive+1))
	if e != nil || n > MaxArchive || hex.EncodeToString(h.Sum(nil)) != t.SHA256 {
		return "", "", ErrInvalid
	}
	if _, e = f.Seek(0, 0); e != nil {
		return "", "", e
	}
	buffer := bufio.NewReader(f)
	gz, e := gzip.NewReader(buffer)
	if e != nil {
		return "", "", e
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(contextReader{ctx, gz})
	name = t.ID + ".binary"
	_ = s.root.Remove(name)
	out, e := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", "", e
	}
	success := false
	defer func() {
		out.Close()
		if !success {
			_ = s.root.Remove(name)
		}
	}()
	count := 0
	var expanded int64
	bh := sha256.New()
	for {
		if e = ctx.Err(); e != nil {
			return "", "", e
		}
		hd, x := tr.Next()
		if x == io.EOF {
			break
		}
		if x != nil {
			return "", "", x
		}
		cleaned := path.Clean(hd.Name)
		if cleaned == "." || strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(hd.Name, "\\") {
			return "", "", ErrInvalid
		}
		if hd.Size < 0 || hd.Size > MaxBinary || expanded+hd.Size > MaxBinary {
			return "", "", ErrInvalid
		}
		expanded += hd.Size
		if hd.Typeflag != tar.TypeReg && hd.Typeflag != tar.TypeDir {
			return "", "", ErrInvalid
		}
		if path.Base(cleaned) == "pipewright" && hd.Typeflag == tar.TypeReg {
			count++
			if count != 1 || hd.Size == 0 {
				return "", "", ErrInvalid
			}
			n, x = io.Copy(io.MultiWriter(out, bh), io.LimitReader(tr, MaxBinary+1))
			if x != nil || n != hd.Size {
				return "", "", ErrInvalid
			}
		}
	}
	// tar EOF precedes the gzip trailer. A bounded all-zero tar tail and a
	// successful gzip EOF are required before any candidate becomes installable.
	tail, e := io.ReadAll(io.LimitReader(contextReader{ctx, gz}, MaxManifest+1))
	if e != nil || int64(len(tail)) > MaxManifest || count != 1 {
		return "", "", ErrInvalid
	}
	for _, v := range tail {
		if v != 0 {
			return "", "", ErrInvalid
		}
	}
	if _, e = buffer.ReadByte(); e != io.EOF {
		return "", "", ErrInvalid
	}
	if e = out.Sync(); e != nil {
		return "", "", e
	}
	if e = out.Close(); e != nil {
		return "", "", e
	}
	digest = hex.EncodeToString(bh.Sum(nil))
	success = true
	return name, digest, nil
}
