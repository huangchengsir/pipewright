package terminalhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memorySession struct {
	r       io.Reader
	mu      sync.Mutex
	input   bytes.Buffer
	closed  atomic.Bool
	resizes atomic.Int32
	short   bool
}

func (s *memorySession) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *memorySession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.short && len(p) > 1 {
		p = p[:1]
	}
	return s.input.Write(p)
}
func (s *memorySession) Close() error {
	s.closed.Store(true)
	if c, ok := s.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
func (s *memorySession) Resize(int, int) error { s.resizes.Add(1); return nil }
func testOpen(s Session) Opener                { return func(context.Context) (Session, error) { return s, nil } }

type eofDuringWriteSession struct {
	r        *io.PipeReader
	w        *io.PipeWriter
	afterEOF func()
	output   []byte
}

func (s *eofDuringWriteSession) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *eofDuringWriteSession) Write(p []byte) (int, error) {
	if _, err := s.w.Write(s.output); err != nil {
		return 0, err
	}
	_ = s.w.Close()
	s.afterEOF()
	return len(p), nil
}
func (s *eofDuringWriteSession) Resize(int, int) error { return nil }
func (s *eofDuringWriteSession) Close() error          { return s.r.Close() }

func TestCompletedInputACKSurvivesNormalEOFButNotCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal EOF", true: "canceled write"}[canceled], func(t *testing.T) {
			m := New()
			defer m.Close()
			r, w := io.Pipe()
			output := []byte("last command result\r\n")
			s := &eofDuringWriteSession{r: r, w: w, output: output}
			id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", testOpen(s), nil)
			if err != nil {
				t.Fatal(err)
			}
			e, _ := m.lookup("a", "s", id)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.afterEOF = func() {
				for {
					e.mu.Lock()
					closed := e.closed
					notify := e.notify
					e.mu.Unlock()
					if closed {
						break
					}
					<-notify
				}
				if canceled {
					cancel()
				}
			}
			err = m.Write(ctx, "a", "s", id, Input{Seq: 1, Data: []byte("exit\n")})
			if canceled {
				if !errors.Is(err, ErrClosed) {
					t.Fatalf("canceled write ACKed: %v", err)
				}
				if err = m.Write(context.Background(), "a", "s", id, Input{Seq: 2, Data: []byte("new command\n")}); !errors.Is(err, ErrClosed) {
					t.Fatalf("cancellation misclassified as normal EOF: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("fully delivered input falsely uncertain after EOF: %v", err)
			}
			out, err := m.Poll(context.Background(), "a", "s", id, 0, nil)
			if err != nil || !out.Closed || !bytes.Equal(out.Data, output) {
				t.Fatalf("final output lost: %+v %v", out, err)
			}
			e.mu.Lock()
			seq := e.seq
			e.mu.Unlock()
			if seq != 1 {
				t.Fatalf("input ACK sequence = %d", seq)
			}
			if err = m.Write(context.Background(), "a", "s", id, Input{Seq: 1, Data: []byte("exit\n")}); err != nil {
				t.Fatalf("completed duplicate lost its ACK after EOF: %v", err)
			}
			if err = m.Write(context.Background(), "a", "s", id, Input{Seq: 1, Data: []byte("different\n")}); !errors.Is(err, ErrConflict) {
				t.Fatalf("duplicate digest conflict lost after EOF: %v", err)
			}
			if err = m.Write(context.Background(), "a", "s", id, Input{Seq: 2, Data: []byte("new command\n")}); !errors.Is(err, ErrEnded) {
				t.Fatalf("new input after EOF = %v", err)
			}
		})
	}
}

type terminalReadFailure struct{}

func (terminalReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type eofDuringResizeSession struct{ *eofDuringWriteSession }

func (s *eofDuringResizeSession) Resize(int, int) error {
	if _, err := s.Write(nil); err != nil {
		return err
	}
	return io.EOF
}

type resizeResultSession struct {
	*memorySession
	resizeErr error
	onResize  func()
}

func (s *resizeResultSession) Resize(int, int) error {
	s.resizes.Add(1)
	if s.onResize != nil {
		s.onResize()
	}
	return s.resizeErr
}

func TestResizeEOFAheadOfBackpressuredReaderPreservesFinalDrain(t *testing.T) {
	for _, result := range []string{"channel EOF", "canceled resize", "generic resize failure"} {
		t.Run(result, func(t *testing.T) {
			m := New()
			defer m.Close()
			output := bytes.Repeat([]byte("large final output 中\r\n"), 100000)
			s := &resizeResultSession{memorySession: &memorySession{r: bytes.NewReader(output)}, resizeErr: io.EOF}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch result {
			case "canceled resize":
				s.onResize = cancel
			case "generic resize failure":
				s.resizeErr = errors.New("resize rejected")
			}
			id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", testOpen(s), nil)
			if err != nil {
				t.Fatal(err)
			}
			e, _ := m.lookup("a", "s", id)
			deadline := time.Now().Add(time.Second)
			for {
				e.mu.Lock()
				full, closed := len(e.buf) == bufferLimit, e.closed
				e.mu.Unlock()
				if closed {
					t.Fatal("reader reached EOF before buffer filled")
				}
				if full {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("output buffer did not fill")
				}
				time.Sleep(time.Millisecond)
			}
			err = m.Write(ctx, "a", "s", id, Input{Seq: 1, Cols: 120, Rows: 40})
			if result != "channel EOF" {
				if !errors.Is(err, ErrClosed) {
					t.Fatalf("non-normal resize = %v, want ErrClosed", err)
				}
				e.mu.Lock()
				closed, aborted := e.closed, e.aborted
				e.mu.Unlock()
				if !closed || !aborted {
					t.Fatal("non-normal resize left a live session with input disabled")
				}
				return
			}
			if !errors.Is(err, ErrEnded) {
				t.Fatalf("channel EOF ahead of reader = %v, want ErrEnded", err)
			}
			e.mu.Lock()
			closed, aborted := e.closed, e.aborted
			e.mu.Unlock()
			if closed || aborted {
				t.Fatal("resize EOF interrupted unread final output")
			}
			pollCtx, stopPoll := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopPoll()
			var got []byte
			var after int64
			for {
				out, err := m.Poll(pollCtx, "a", "s", id, after, nil)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, out.Data...)
				after = out.Next
				if out.Closed {
					break
				}
			}
			if !bytes.Equal(got, output) {
				t.Fatalf("backpressured final output lost: %d != %d", len(got), len(output))
			}
		})
	}
}

func TestResizeRacingNormalEOFLeavesFinalOutputDrainable(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal EOF", true: "canceled resize"}[canceled], func(t *testing.T) {
			m := New()
			defer m.Close()
			r, w := io.Pipe()
			output := []byte("last result before EOF\r\n")
			s := &eofDuringResizeSession{&eofDuringWriteSession{r: r, w: w, output: output}}
			id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", testOpen(s), nil)
			if err != nil {
				t.Fatal(err)
			}
			e, _ := m.lookup("a", "s", id)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.afterEOF = func() {
				for {
					e.mu.Lock()
					closed, notify := e.closed, e.notify
					e.mu.Unlock()
					if closed {
						break
					}
					<-notify
				}
				if canceled {
					cancel()
				}
			}
			err = m.Write(ctx, "a", "s", id, Input{Seq: 1, Cols: 120, Rows: 40})
			want := ErrEnded
			if canceled {
				want = ErrClosed
			}
			if !errors.Is(err, want) {
				t.Fatalf("resize result = %v, want %v", err, want)
			}
			out, err := m.Poll(context.Background(), "a", "s", id, 0, nil)
			if err != nil || !out.Closed || !bytes.Equal(out.Data, output) {
				t.Fatalf("final resize output lost: %+v %v", out, err)
			}
		})
	}
}

func TestInputAfterEOFIsUnwrittenAndFinalOutputRemainsDrainable(t *testing.T) {
	for _, ending := range []string{"normal EOF", "read failure", "explicit close", "canceled request"} {
		t.Run(ending, func(t *testing.T) {
			m := New()
			defer m.Close()
			output := bytes.Repeat([]byte("final result 中\r\n"), 10000)
			var reader io.Reader = bytes.NewReader(output)
			if ending == "read failure" {
				reader = io.MultiReader(reader, terminalReadFailure{})
			}
			s := &memorySession{r: reader}
			id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", testOpen(s), nil)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for !s.closed.Load() {
				if time.Now().After(deadline) {
					t.Fatal("output reader did not finish")
				}
				time.Sleep(time.Millisecond)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ErrEnded
			switch ending {
			case "read failure":
				want = ErrClosed
			case "explicit close":
				if err = m.Delete("a", "s", id); err != nil {
					t.Fatal(err)
				}
				want = ErrClosed
			case "canceled request":
				cancel()
				want = ErrClosed
			}
			for _, input := range []Input{{Seq: 1, Data: []byte("new command\n")}, {Seq: 1, Cols: 120, Rows: 40}} {
				if err = m.Write(ctx, "a", "s", id, input); !errors.Is(err, want) {
					t.Fatalf("input %+v after %s = %v, want %v", input, ending, err, want)
				}
			}
			s.mu.Lock()
			written := s.input.Len()
			s.mu.Unlock()
			if written != 0 || s.resizes.Load() != 0 {
				t.Fatalf("ended session received input: %d bytes, %d resizes", written, s.resizes.Load())
			}
			if ending != "normal EOF" {
				return
			}
			var got []byte
			var after int64
			for {
				out, err := m.Poll(context.Background(), "a", "s", id, after, nil)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, out.Data...)
				after = out.Next
				if out.Closed {
					break
				}
			}
			if !bytes.Equal(got, output) {
				t.Fatalf("final output lost after rejected input: %d != %d", len(got), len(output))
			}
		})
	}
}

type orderedCloseSession struct {
	r                *io.PipeReader
	ctx              context.Context
	entered, release chan struct{}
	ctxWasLive       atomic.Bool
}

func (s *orderedCloseSession) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *orderedCloseSession) Write(p []byte) (int, error) { return len(p), nil }
func (s *orderedCloseSession) Resize(int, int) error       { return nil }
func (s *orderedCloseSession) Close() error {
	s.ctxWasLive.Store(s.ctx.Err() == nil)
	close(s.entered)
	<-s.release
	return s.r.Close()
}

func TestDeleteClosesSessionBeforeCancelAndManagerWaitsForClose(t *testing.T) {
	m := New()
	r, w := io.Pipe()
	defer w.Close()
	s := &orderedCloseSession{r: r, entered: make(chan struct{}), release: make(chan struct{})}
	id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", func(ctx context.Context) (Session, error) { s.ctx = ctx; return s, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Delete("a", "s", id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.entered:
	case <-time.After(time.Second):
		t.Fatal("Close not called")
	}
	if !s.ctxWasLive.Load() || s.ctx.Err() != nil {
		t.Fatal("lifetime canceled before session Close")
	}
	managerDone := make(chan struct{})
	go func() { m.Close(); close(managerDone) }()
	select {
	case <-managerDone:
		t.Fatal("manager returned before session Close completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(s.release)
	select {
	case <-managerDone:
	case <-time.After(time.Second):
		t.Fatal("manager did not join session Close")
	}
	if s.ctx.Err() == nil {
		t.Fatal("lifetime still live after Close")
	}
}

func TestManagerCloseJoinsLateCanceledOpenerPTYCleanup(t *testing.T) {
	m := New()
	r, w := io.Pipe()
	defer w.Close()
	s := &orderedCloseSession{r: r, entered: make(chan struct{}), release: make(chan struct{})}
	opening := make(chan struct{})
	created := make(chan error, 1)
	go func() {
		_, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", func(ctx context.Context) (Session, error) { s.ctx = ctx; close(opening); <-ctx.Done(); return s, nil }, nil)
		created <- err
	}()
	<-opening
	managerDone := make(chan struct{})
	go func() { m.Close(); close(managerDone) }()
	select {
	case <-s.entered:
	case <-time.After(time.Second):
		t.Fatal("late PTY cleanup not started")
	}
	select {
	case <-managerDone:
		t.Fatal("manager returned while late PTY cleanup still blocked")
	case <-time.After(20 * time.Millisecond):
	}
	close(s.release)
	select {
	case <-managerDone:
	case <-time.After(time.Second):
		t.Fatal("manager did not join late PTY cleanup")
	}
	if err := <-created; !errors.Is(err, ErrClosed) {
		t.Fatalf("canceled creation returned %v", err)
	}
}

func TestOutputBackpressureReplayAndFinalDrain(t *testing.T) {
	m := New()
	defer m.Close()
	data := bytes.Repeat([]byte("中\x00\xff\x1b[31m"), 450000) // >4 MiB, including split UTF-8.
	s := &memorySession{r: bytes.NewReader(data)}
	id, err := m.Create(context.Background(), "a", "host", "0123456789abcdef", "sh", testOpen(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got []byte
	var after int64
	for {
		out, err := m.Poll(ctx, "a", "host", id, after, func() bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Data) > responseLimit {
			t.Fatal("response unbounded")
		}
		if len(got) == 0 {
			replay, err := m.Poll(ctx, "a", "host", id, after, nil)
			if err != nil || len(replay.Data) < len(out.Data) || !bytes.Equal(replay.Data[:len(out.Data)], out.Data) {
				t.Fatalf("replay differs %v", err)
			}
		}
		got = append(got, out.Data...)
		after = out.Next
		if out.Closed {
			break
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("output differs: %d != %d", len(got), len(data))
	}
}

func TestEOFSlowDrainOutlivesTombstoneWindow(t *testing.T) {
	m := New()
	defer m.Close()
	data := bytes.Repeat([]byte("x"), 128<<10)
	id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", testOpen(&memorySession{r: bytes.NewReader(data)}), nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err := m.lookup("a", "s", id)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		e.mu.Lock()
		closed := e.closed
		if closed {
			e.ended = time.Now().Add(-2 * retention)
			e.last = time.Now()
		}
		e.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("EOF not observed")
		}
		time.Sleep(time.Millisecond)
	}
	m.sweep(time.Now())
	if _, err = m.lookup("a", "s", id); err != nil {
		t.Fatal("live final drain evicted", err)
	}
	var got []byte
	var after int64
	for {
		out, err := m.Poll(context.Background(), "a", "s", id, after, nil)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, out.Data...)
		after = out.Next
		if out.Closed {
			break
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatal("final bytes lost")
	}
}

func TestNewlyFullBufferGetsFullStallGraceAfterIdle(t *testing.T) {
	m := New()
	defer m.Close()
	r, w := io.Pipe()
	defer w.Close()
	id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", testOpen(&memorySession{r: r}), nil)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := m.lookup("a", "s", id)
	e.mu.Lock()
	e.progress = time.Now().Add(-time.Hour)
	e.mu.Unlock()
	go func() { _, _ = w.Write(bytes.Repeat([]byte("x"), bufferLimit+1)) }()
	deadline := time.Now().Add(time.Second)
	for {
		e.mu.Lock()
		full := len(e.buf) == bufferLimit
		e.mu.Unlock()
		if full {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("buffer did not fill")
		}
		time.Sleep(time.Millisecond)
	}
	m.sweep(time.Now())
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		t.Fatal("new output inherited old idle progress timestamp")
	}
}
func TestCreateConcurrentIdempotencyAndIsolation(t *testing.T) {
	m := New()
	defer m.Close()
	var opens, audits atomic.Int32
	start := make(chan struct{})
	release := make(chan struct{})
	opener := func(context.Context) (Session, error) {
		opens.Add(1)
		close(start)
		<-release
		r, _ := io.Pipe()
		return &memorySession{r: r}, nil
	}
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			id, err := m.Create(context.Background(), "owner", "scope", "0123456789abcdef", "sh", opener, func() { audits.Add(1) })
			ids <- id
			errs <- err
		}()
	}
	<-start
	close(release)
	a, b := <-ids, <-ids
	if a == "" || a != b || <-errs != nil || <-errs != nil || opens.Load() != 1 || audits.Load() != 1 {
		t.Fatal("duplicate creation")
	}
	if _, err := m.Create(context.Background(), "owner", "different", "0123456789abcdef", "sh", opener, nil); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := m.Poll(context.Background(), "other", "scope", a, 0, nil); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := m.Delete("owner", "wrong", a); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func TestInputOrderingShortWritesAndCursor(t *testing.T) {
	m := New()
	defer m.Close()
	r, w := io.Pipe()
	defer w.Close()
	s := &memorySession{r: r, short: true}
	id, err := m.Create(context.Background(), "a", "host", "0123456789abcdef", "sh", testOpen(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Seq: 1, Data: []byte("hello")}
	if err = m.Write(context.Background(), "a", "host", id, in); err != nil {
		t.Fatal(err)
	}
	if err = m.Write(context.Background(), "a", "host", id, in); err != nil {
		t.Fatal(err)
	}
	in.Data = []byte("different")
	if err = m.Write(context.Background(), "a", "host", id, in); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	s.mu.Lock()
	got := s.input.String()
	s.mu.Unlock()
	if got != "hello" {
		t.Fatal(got)
	}
	if _, err = m.Poll(context.Background(), "a", "host", id, 10, nil); !errors.Is(err, ErrCursor) {
		t.Fatal(err)
	}
	if err = m.Write(context.Background(), "a", "host", id, Input{Seq: 2, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
}
func TestLeaseQuotasAndRetainedTombstones(t *testing.T) {
	m := New()
	defer m.Close()
	for n := 0; n < 4; n++ {
		r, _ := io.Pipe()
		_, err := m.Create(context.Background(), "a", "s", string(rune('a'+n))+"123456789abcdef", "sh", testOpen(&memorySession{r: r}), nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Create(context.Background(), "a", "s", "over123456789abcdef", "sh", nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	m.mu.Lock()
	for _, e := range m.entries {
		e.mu.Lock()
		e.last = time.Now().Add(-46 * time.Second)
		e.mu.Unlock()
	}
	m.mu.Unlock()
	m.sweep(time.Now())
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) != 4 {
		t.Fatal("tombstones evicted")
	}
	for _, e := range m.entries {
		e.mu.Lock()
		closed := e.closed
		e.mu.Unlock()
		if !closed {
			t.Fatal("lease did not close")
		}
	}
}
func TestRevokedLoginDoesNotReleaseOutput(t *testing.T) {
	m := New()
	defer m.Close()
	id, err := m.Create(context.Background(), "a", "s", "0123456789abcdef", "sh", testOpen(&memorySession{r: bytes.NewBufferString("secret")}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := m.Poll(context.Background(), "a", "s", id, 0, func() bool { return false }); err == nil || len(out.Data) > 0 {
		t.Fatal("revoked login received bytes")
	}
}
