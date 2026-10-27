// Package terminalhttp owns bounded, transient interactive terminal sessions.
// It deliberately does not know about HTTP, cookies, credentials, or WebSockets.
package terminalhttp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("terminal session not found")
	ErrConflict = errors.New("terminal request conflict")
	ErrLimit    = errors.New("terminal capacity reached")
	ErrClosed   = errors.New("terminal session closed")
	// ErrEnded means the terminal ended before accepting this input or resize.
	// Final output, including bytes not yet read, remains available for polling.
	ErrEnded  = errors.New("terminal session ended normally")
	ErrCursor = errors.New("terminal output cursor invalid")
	ErrInput  = errors.New("terminal input invalid")
)

const (
	bufferLimit   = 1 << 20
	responseLimit = 64 << 10
	lease         = 45 * time.Second
	retention     = 60 * time.Second
)

type Session interface {
	io.ReadWriteCloser
	Resize(int, int) error
}
type Opener func(context.Context) (Session, error)
type Output struct {
	Data   []byte
	Next   int64
	Closed bool
	Reason string
}
type Input struct {
	Seq        int64
	Data       []byte
	Cols, Rows int
}

type entry struct {
	mu                    sync.Mutex
	writeMu               sync.Mutex
	owner, scope, key, id string
	digest                [32]byte
	ready                 chan struct{}
	notify                chan struct{}
	ctx                   context.Context
	cancel                context.CancelFunc
	session               Session
	err                   error
	closed                bool
	normalEOF             bool
	aborted               bool
	closeDone             chan struct{}
	reason                string
	last, progress, ended time.Time
	buf                   []byte
	base, sent            int64
	seq                   int64
	inputHash             [32]byte
	polling               bool
}

type Manager struct {
	admit     func(context.Context) (func(), error)
	mu        sync.Mutex
	entries   map[string]*entry
	keys      map[string]*entry
	closed    bool
	lifetimes sync.WaitGroup
}

func New() *Manager { return &Manager{entries: make(map[string]*entry), keys: make(map[string]*entry)} }

func NewWithAdmission(admit func(context.Context) (func(), error)) *Manager {
	m := New()
	m.admit = admit
	return m
}

// Run is explicitly owned by the composition root, never by the router.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.Close()
			return
		case now := <-t.C:
			m.sweep(now)
		}
	}
}
func (m *Manager) sweep(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.entries {
		e.mu.Lock()
		if !e.closed && (now.Sub(e.last) >= lease || (len(e.buf) >= bufferLimit && now.Sub(e.progress) >= lease)) {
			e.finishLocked("session expired")
		}
		if e.closed && len(e.buf) > 0 && now.Sub(e.last) >= lease {
			e.buf = nil
			e.base = e.sent
			e.ended = now
		}
		if e.closed && len(e.buf) == 0 && !e.ended.IsZero() && now.Sub(e.ended) >= retention {
			delete(m.entries, id)
			delete(m.keys, e.key)
		}
		e.mu.Unlock()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, e := range m.entries {
		e.mu.Lock()
		e.finishLocked("session ended")
		e.mu.Unlock()
	}
	m.mu.Unlock()
	// Join the entire admitted lifetime, including failed opening, actual output
	// reader exit and release bookkeeping. Swept entries are still included.
	m.lifetimes.Wait()
}
func (e *entry) wakeLocked() { close(e.notify); e.notify = make(chan struct{}) }
func (e *entry) finishLocked(reason string) {
	// Explicit close, expiry and uncertain input invalidate in-flight input even
	// if the output reader has already observed a normal EOF.
	e.aborted = true
	e.closeLocked(reason)
}
func (e *entry) closeLocked(reason string) {
	if e.closed {
		return
	}
	e.closed = true
	e.reason = reason
	e.ended = time.Now()
	if e.session != nil {
		// Close gets a chance to signal the remote process while the lifetime
		// context is still live. Production Session.Close bounds stalled writes.
		go func() { defer close(e.closeDone); defer e.cancel(); _ = e.session.Close() }()
	} else {
		e.cancel()
		close(e.closeDone)
	}
	e.wakeLocked()
}

// Create reserves both quota and its idempotency key before opening an SSH PTY.
// onOpen runs exactly once and must not include terminal contents or credentials.
func (m *Manager) Create(ctx context.Context, owner, scope, requestID, description string, open Opener, onOpen func()) (string, error) {
	if len(requestID) < 16 || len(requestID) > 128 {
		return "", ErrInput
	}
	key := owner + "\x00" + requestID
	digest := sha256.Sum256([]byte(scope + "\x00" + description))
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", ErrClosed
	}
	if e := m.keys[key]; e != nil {
		m.mu.Unlock()
		if e.digest != digest {
			return "", ErrConflict
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-e.ready:
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.err != nil {
			return "", e.err
		}
		if e.closed {
			return "", ErrClosed
		}
		return e.id, nil
	}
	active, owned := 0, 0
	for _, e := range m.entries {
		e.mu.Lock()
		live := !e.closed || len(e.buf) > 0
		e.mu.Unlock()
		if live {
			active++
			if e.owner == owner {
				owned++
			}
		}
	}
	if len(m.entries) >= 64 || active >= 32 || owned >= 4 {
		m.mu.Unlock()
		return "", ErrLimit
	}
	release := func() {}
	if m.admit != nil {
		var err error
		release, err = m.admit(ctx)
		if err != nil {
			m.mu.Unlock()
			return "", err
		}
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		release()
		m.mu.Unlock()
		return "", err
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	e := &entry{owner: owner, scope: scope, key: key, id: hex.EncodeToString(raw), digest: digest, ready: make(chan struct{}), notify: make(chan struct{}), closeDone: make(chan struct{}), ctx: sessionCtx, cancel: cancel, last: now, progress: now}
	m.entries[e.id] = e
	m.keys[key] = e
	m.lifetimes.Add(1) // Add under mu; Close forbids all later admissions.
	m.mu.Unlock()
	// The creation timer cancels the lifetime context only while opening. Stopping
	// it after success preserves the established session's independent lifetime.
	expired := make(chan struct{})
	timer := time.AfterFunc(15*time.Second, func() { cancel(); close(expired) })
	sess, err := open(sessionCtx)
	if !timer.Stop() {
		<-expired
	}
	e.mu.Lock()
	if err == nil && (sessionCtx.Err() != nil || e.closed) {
		err = ErrClosed
	}
	e.err = err
	if err != nil {
		e.finishLocked("session ended")
		if sess != nil {
			// A canceled opener may still return a PTY. Finish disposing it before
			// publishing ready, which is the manager shutdown join point.
			_ = sess.Close()
		}
	} else {
		e.session = sess
		e.last = time.Now()
		if onOpen != nil {
			onOpen()
		}
	}
	close(e.ready)
	e.mu.Unlock()
	if err != nil {
		defer m.lifetimes.Done()
		release()
		return "", err
	}
	go func() { defer m.lifetimes.Done(); defer func() { <-e.closeDone; release() }(); e.read() }()
	return e.id, nil
}
func (m *Manager) lookup(owner, scope, id string) (*entry, error) {
	m.mu.Lock()
	e := m.entries[id]
	m.mu.Unlock()
	if e == nil || e.owner != owner || e.scope != scope {
		return nil, ErrNotFound
	}
	return e, nil
}
func (e *entry) read() {
	tmp := make([]byte, 32<<10)
	for {
		e.mu.Lock()
		for len(e.buf) >= bufferLimit && !e.closed {
			wait := e.notify
			e.mu.Unlock()
			select {
			case <-wait:
			case <-e.ctx.Done():
			}
			e.mu.Lock()
		}
		if e.closed {
			e.mu.Unlock()
			return
		}
		room := bufferLimit - len(e.buf)
		e.mu.Unlock()
		n, err := e.session.Read(tmp[:min(room, len(tmp))])
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return
		}
		if n > 0 {
			wasFull := len(e.buf) >= bufferLimit
			e.buf = append(e.buf, tmp[:n]...)
			if !wasFull && len(e.buf) >= bufferLimit {
				e.progress = time.Now()
			}
			e.wakeLocked()
		}
		if err != nil {
			e.normalEOF = errors.Is(err, io.EOF)
			e.closeLocked("session ended")
		}
		e.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// Poll's cursor is also an ACK: bytes are freed only on the next valid request.
// auth must revalidate the login before releasing output and renewing its lease.
func (m *Manager) Poll(ctx context.Context, owner, scope, id string, after int64, auth func() bool) (Output, error) {
	e, err := m.lookup(owner, scope, id)
	if err != nil {
		return Output{}, err
	}
	e.mu.Lock()
	if e.polling {
		e.mu.Unlock()
		return Output{}, ErrConflict
	}
	e.polling = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.polling = false; e.mu.Unlock() }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		e.mu.Lock()
		if after < e.base || after > e.sent {
			e.mu.Unlock()
			return Output{}, ErrCursor
		}
		if after > e.base {
			drop := int(after - e.base)
			e.buf = append([]byte(nil), e.buf[drop:]...)
			e.base = after
			e.progress = time.Now()
			e.wakeLocked()
		}
		if len(e.buf) > 0 || e.closed {
			e.mu.Unlock()
			if auth != nil && !auth() {
				return Output{}, ErrNotFound
			}
			e.mu.Lock()
			n := min(len(e.buf), responseLimit)
			out := Output{Data: append([]byte(nil), e.buf[:n]...), Next: e.base + int64(n), Closed: e.closed && n == len(e.buf), Reason: e.reason}
			if out.Next > e.sent {
				e.sent = out.Next
			}
			e.last = time.Now()
			e.mu.Unlock()
			return out, nil
		}
		wait := e.notify
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		case <-timer.C:
			if auth != nil && !auth() {
				return Output{}, ErrNotFound
			}
			e.mu.Lock()
			e.last = time.Now()
			out := Output{Next: e.base}
			e.mu.Unlock()
			return out, nil
		case <-wait:
		}
	}
}
func (m *Manager) Write(ctx context.Context, owner, scope, id string, in Input) error {
	if ctx.Err() != nil {
		return ErrClosed
	}
	if in.Seq < 1 || len(in.Data) > 16<<10 || (len(in.Data) > 0 && (in.Cols != 0 || in.Rows != 0)) || (len(in.Data) == 0 && (in.Cols < 1 || in.Rows < 1 || in.Cols > 1000 || in.Rows > 1000)) {
		return ErrInput
	}
	e, err := m.lookup(owner, scope, id)
	if err != nil {
		return err
	}
	if !e.writeMu.TryLock() {
		return ErrConflict
	}
	defer e.writeMu.Unlock()
	h := sha256.New()
	h.Write(in.Data)
	h.Write([]byte{byte(in.Cols >> 8), byte(in.Cols), byte(in.Rows >> 8), byte(in.Rows)})
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	e.mu.Lock()
	if e.closed || e.session == nil {
		if e.closed && e.normalEOF && !e.aborted {
			// Preserve the ACK for an input already completed during EOF; a
			// duplicate must never be described as an unwritten new input.
			if in.Seq == e.seq {
				ok := digest == e.inputHash
				e.mu.Unlock()
				if ok {
					return nil
				}
				return ErrConflict
			}
			e.mu.Unlock()
			return ErrEnded
		}
		e.mu.Unlock()
		return ErrClosed
	}
	if in.Seq == e.seq {
		ok := digest == e.inputHash
		e.mu.Unlock()
		if ok {
			return nil
		}
		return ErrConflict
	}
	if in.Seq != e.seq+1 {
		e.mu.Unlock()
		return ErrConflict
	}
	sess := e.session
	e.mu.Unlock()
	// A hung write cannot pin a handler or a PTY forever.
	interrupt := func() { e.mu.Lock(); e.finishLocked("input result uncertain"); e.mu.Unlock() }
	timedOut := make(chan struct{})
	timer := time.AfterFunc(15*time.Second, func() { defer close(timedOut); interrupt() })
	contextFinished := make(chan struct{})
	stopContext := context.AfterFunc(ctx, func() { defer close(contextFinished); interrupt() })
	if len(in.Data) > 0 {
		data := in.Data
		for len(data) > 0 {
			n, werr := sess.Write(data)
			if n < 0 || n > len(data) {
				werr = io.ErrShortWrite
				n = 0
			}
			data = data[n:]
			if werr != nil {
				err = werr
				break
			}
			if n == 0 {
				err = io.ErrShortWrite
				break
			}
		}
	} else {
		err = sess.Resize(in.Cols, in.Rows)
	}
	if !timer.Stop() {
		<-timedOut
	}
	if !stopContext() {
		<-contextFinished
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// A resize cannot execute a command. Its channel may report EOF before the
	// output reader gets there, especially when unread output fills the buffer.
	// Preserve that drain, but never treat a live session's generic resize error
	// or an explicitly interrupted session as a normal end.
	resizeEnded := (e.closed && e.normalEOF) || (!e.closed && errors.Is(err, io.EOF))
	if err != nil && len(in.Data) == 0 && ctx.Err() == nil && !e.aborted && resizeEnded {
		return ErrEnded
	}
	if err != nil || ctx.Err() != nil || (e.closed && (!e.normalEOF || e.aborted)) {
		e.finishLocked("input result uncertain")
		return ErrClosed
	}
	e.seq = in.Seq
	e.inputHash = digest
	e.last = time.Now()
	return nil
}
func (m *Manager) Delete(owner, scope, id string) error {
	e, err := m.lookup(owner, scope, id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.finishLocked("session ended")
	if len(e.buf) > 0 {
		e.ended = time.Now()
	}
	e.buf = nil
	e.base = e.sent
	e.mu.Unlock()
	return nil
}
