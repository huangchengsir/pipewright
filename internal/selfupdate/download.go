package selfupdate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var checksumPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func (s *Service) assetURL(t Task, name string) string {
	base := s.opts.BaseURL
	if base == "" {
		base = "https://github.com"
	}
	return strings.TrimRight(base, "/") + "/" + t.Repository + "/releases/download/" + t.Target + "/" + name
}
func (s *Service) checksum(ctx context.Context, t Task) (string, error) {
	ctx, cancel, touch := idleContext(ctx, s.opts.IdleTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, s.assetURL(t, "checksums.txt"), nil)
	if e != nil {
		return "", e
	}
	res, e := s.do(req)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(progressReader{Reader: res.Body, touch: touch}, MaxManifest+1))
	if e != nil || int64(len(b)) > MaxManifest {
		return "", ErrInvalid
	}
	scan := bufio.NewScanner(strings.NewReader(string(b)))
	found := ""
	seen := map[string]bool{}
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || !checksumPattern.MatchString(f[0]) {
			return "", ErrInvalid
		}
		name := strings.TrimPrefix(f[1], "*")
		if name == "" || strings.ContainsAny(name, "/\\") || seen[name] {
			return "", ErrInvalid
		}
		seen[name] = true
		if name == t.Asset {
			found = strings.ToLower(f[0])
		}
	}
	if scan.Err() != nil || found == "" {
		return "", ErrInvalid
	}
	return found, nil
}
func (s *Service) download(parent context.Context, t *Task) error {
	ctx, cancel, touch := idleContext(parent, s.opts.IdleTimeout)
	defer cancel()
	name := t.ID + ".part"
	f, e := s.openPrivate(name)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > MaxArchive {
		return ErrInvalid
	}
	offset := st.Size()
	// Only a fully downloaded, digest-bound package skips network on continue.
	if offset > 0 && t.Total > 0 && offset == t.Total {
		if _, e = f.Seek(0, 0); e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, io.LimitReader(f, MaxArchive+1))
		if e == nil && hex.EncodeToString(h.Sum(nil)) == t.SHA256 {
			t.Bytes = offset
			return nil
		}
	}
	if offset > 0 && (t.ETag == "" || strings.HasPrefix(t.ETag, "W/")) {
		offset = 0
	}
	if offset == 0 {
		if e = f.Truncate(0); e != nil {
			return e
		}
		t.Bytes = 0
		t.Total = 0
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, s.assetURL(*t, t.Asset), nil)
	if e != nil {
		return e
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		req.Header.Set("If-Range", t.ETag)
	}
	res, e := s.do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if enc := res.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return ErrInvalid
	}
	if res.StatusCode == http.StatusPartialContent {
		if offset <= 0 || res.Header.Get("ETag") != t.ETag {
			return ErrInvalid
		}
		start, end, total, e := parseRange(res.Header.Get("Content-Range"))
		if e != nil || start != offset || total > MaxArchive || (t.Total > 0 && total != t.Total) || end != total-1 || res.ContentLength >= 0 && res.ContentLength != end-start+1 {
			return ErrInvalid
		}
		t.Total = total
	} else if res.StatusCode == http.StatusOK {
		offset = 0
		if e = f.Truncate(0); e != nil {
			return e
		}
		if res.ContentLength > MaxArchive {
			return ErrInvalid
		}
		t.Total = res.ContentLength
		if t.Total < 0 {
			t.Total = 0
		}
		t.ETag = res.Header.Get("ETag")
		if strings.HasPrefix(t.ETag, "W/") {
			t.ETag = ""
		}
	} else {
		return ErrUnavailable
	}
	if _, e = f.Seek(offset, 0); e != nil {
		return e
	}
	t.Bytes = offset
	if e = s.save(ctx, t); e != nil {
		return e
	}
	buf := make([]byte, 64<<10)
	saved := time.Now()
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if t.Bytes+int64(n) > MaxArchive {
				return ErrInvalid
			}
			wn, werr := f.Write(buf[:n])
			if werr != nil || wn != n {
				return errors.New("download storage failed")
			}
			t.Bytes += int64(n)
			touch()
			if time.Since(saved) >= 250*time.Millisecond {
				if e = s.save(ctx, t); e != nil {
					return e
				}
				saved = time.Now()
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return rerr
			}
			break
		}
		if e = ctx.Err(); e != nil {
			return e
		}
	}
	if t.Total > 0 && t.Bytes != t.Total {
		return io.ErrUnexpectedEOF
	}
	t.Total = t.Bytes
	if e = f.Sync(); e != nil {
		return e
	}
	return s.save(ctx, t)
}
func parseRange(v string) (int64, int64, int64, error) {
	if !strings.HasPrefix(v, "bytes ") {
		return 0, 0, 0, ErrInvalid
	}
	p := strings.Split(strings.TrimPrefix(v, "bytes "), "/")
	if len(p) != 2 {
		return 0, 0, 0, ErrInvalid
	}
	r := strings.Split(p[0], "-")
	if len(r) != 2 {
		return 0, 0, 0, ErrInvalid
	}
	a, e := strconv.ParseInt(r[0], 10, 64)
	if e != nil {
		return 0, 0, 0, ErrInvalid
	}
	b, e := strconv.ParseInt(r[1], 10, 64)
	if e != nil {
		return 0, 0, 0, ErrInvalid
	}
	n, e := strconv.ParseInt(p[1], 10, 64)
	if e != nil || a < 0 || b < a || n <= b {
		return 0, 0, 0, ErrInvalid
	}
	return a, b, n, nil
}
