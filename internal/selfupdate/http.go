package selfupdate

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/releasesource"
	"github.com/huangchengsir/pipewright/internal/version"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

func loopbackBase(v string) bool {
	u, e := url.Parse(v)
	return e == nil && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost") && u.RawQuery == "" && u.Fragment == ""
}
func (s *Service) do(req *http.Request, sources ...releasesource.ID) (*http.Response, error) {
	req.Header.Set("User-Agent", "pipewright/"+version.Version)
	source := releasesource.GitHub
	if len(sources) > 0 {
		source = sources[0]
	}
	return releasesource.Do(s.opts.Client, req, s.opts.BaseURL, source)
}

func idleContext(parent context.Context, idle time.Duration) (context.Context, context.CancelFunc, func()) {
	ctx, cancel := context.WithCancel(parent)
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	touch := func() { last.Store(time.Now().UnixNano()) }
	interval := idle / 4
	if interval > time.Second {
		interval = time.Second
	}
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	go func() {
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-timer.C:
				if now.Sub(time.Unix(0, last.Load())) >= idle {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel, touch
}

type progressReader struct {
	io.Reader
	touch func()
}

func (p progressReader) Read(b []byte) (int, error) {
	n, e := p.Reader.Read(b)
	if n > 0 {
		p.touch()
	}
	return n, e
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.Reader.Read(p)
}
