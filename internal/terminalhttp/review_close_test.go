package terminalhttp

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type reviewDelayedReader struct {
	started, readExit, closed chan struct{}
	once                      sync.Once
}

func (s *reviewDelayedReader) Read([]byte) (int, error) {
	close(s.started)
	<-s.readExit
	return 0, io.EOF
}
func (s *reviewDelayedReader) Write(p []byte) (int, error) { return len(p), nil }
func (s *reviewDelayedReader) Resize(int, int) error       { return nil }
func (s *reviewDelayedReader) Close() error                { s.once.Do(func() { close(s.closed) }); return nil }

func reviewAssertClosePending(t *testing.T, done <-chan struct{}, phase string) {
	t.Helper()
	synctest.Wait()
	select {
	case <-done:
		t.Error("Manager.Close returned before " + phase)
	default:
	}
}
func TestReviewCloseJoinsReaderAndReleaseIncludingEvictedEntry(t *testing.T) {
	for _, evict := range []bool{false, true} {
		name := "tracked"
		if evict {
			name = "evicted"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &reviewDelayedReader{make(chan struct{}), make(chan struct{}), make(chan struct{}), sync.Once{}}
				releaseStarted, releaseExit := make(chan struct{}), make(chan struct{})
				m := NewWithAdmission(func(context.Context) (func(), error) { return func() { close(releaseStarted); <-releaseExit }, nil })
				_, e := m.Create(context.Background(), "owner", "scope", "0123456789abcdef", "sh", testOpen(s), nil)
				if e != nil {
					t.Fatal(e)
				}
				synctest.Wait()
				<-s.started
				if evict {
					m.mu.Lock()
					for _, entry := range m.entries {
						entry.mu.Lock()
						entry.finishLocked("test close")
						entry.mu.Unlock()
					}
					m.mu.Unlock()
					synctest.Wait()
					m.sweep(time.Now().Add(2*retention + 2*lease))
					m.mu.Lock()
					n := len(m.entries)
					m.mu.Unlock()
					if n != 0 {
						t.Error("fixture failed to evict completed-looking entry")
					}
				}
				done := make(chan struct{})
				go func() { m.Close(); close(done) }()
				reviewAssertClosePending(t, done, "actual reader exit")
				close(s.readExit)
				synctest.Wait()
				<-releaseStarted
				reviewAssertClosePending(t, done, "maintenance release completion")
				close(releaseExit)
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Error("Manager.Close did not join finished lifetime")
				}
			})
		})
	}
}
func TestReviewCloseJoinsFailedOpenerRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		openStarted, openExit := make(chan struct{}), make(chan struct{})
		releaseStarted, releaseExit := make(chan struct{}), make(chan struct{})
		m := NewWithAdmission(func(context.Context) (func(), error) { return func() { close(releaseStarted); <-releaseExit }, nil })
		created := make(chan struct{})
		go func() {
			defer close(created)
			_, _ = m.Create(context.Background(), "owner", "scope", "0123456789abcdef", "sh", func(context.Context) (Session, error) {
				close(openStarted)
				<-openExit
				return nil, errors.New("synthetic opener failure")
			}, nil)
		}()
		synctest.Wait()
		<-openStarted
		done := make(chan struct{})
		go func() { m.Close(); close(done) }()
		reviewAssertClosePending(t, done, "opener result")
		close(openExit)
		synctest.Wait()
		<-releaseStarted
		reviewAssertClosePending(t, done, "failed opener release completion")
		close(releaseExit)
		synctest.Wait()
		<-created
		select {
		case <-done:
		default:
			t.Error("Manager.Close did not join failed opener lifetime")
		}
	})
}
