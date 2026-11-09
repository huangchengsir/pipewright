package target

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stdioPullFixture struct {
	address, key                          string
	listener                              net.Listener
	mu                                    sync.Mutex
	raw                                   net.Conn
	done, requestSeen, execSeen           chan struct{}
	onceRequest, onceExec                 sync.Once
	sessions, direct, env, requests, auth atomic.Int32
	invalid                               atomic.Bool
}

func newStdioPullFixture(t *testing.T, mode string, auth []byte, image string) *stdioPullFixture {
	t.Helper()
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(private)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	f := &stdioPullFixture{address: listener.Addr().String(), key: ssh.FingerprintSHA256(signer.PublicKey()), listener: listener, done: make(chan struct{}), requestSeen: make(chan struct{}), execSeen: make(chan struct{})}
	t.Cleanup(func() {
		f.close()
		select {
		case <-f.done:
		case <-time.After(2 * time.Second):
			t.Error("private SSH fixture did not drain")
		}
	})
	config := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		f.auth.Add(1)
		if string(password) != "fixture-only" {
			return nil, errors.New("denied")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	go func() {
		defer close(f.done)
		raw, e := listener.Accept()
		if e != nil {
			return
		}
		f.mu.Lock()
		f.raw = raw
		f.mu.Unlock()
		defer raw.Close()
		server, channels, requests, e := ssh.NewServerConn(raw, config)
		if e != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for next := range channels {
			if next.ChannelType() != "session" {
				f.direct.Add(1)
				_ = next.Reject(ssh.Prohibited, "stream-local is disabled for this fixture")
				continue
			}
			f.sessions.Add(1)
			channel, requests, e := next.Accept()
			if e != nil {
				return
			}
			func() {
				defer channel.Close()
				for request := range requests {
					if request.Type == "env" {
						f.env.Add(1)
						_ = request.Reply(false, nil)
						continue
					}
					if request.Type != "exec" {
						f.invalid.Store(true)
						_ = request.Reply(false, nil)
						continue
					}
					var command struct{ Command string }
					if ssh.Unmarshal(request.Payload, &command) != nil || command.Command != "docker --host unix:///var/run/docker.sock --config /dev/null system dial-stdio" {
						f.invalid.Store(true)
						_ = request.Reply(false, nil)
						return
					}
					f.onceExec.Do(func() { close(f.execSeen) })
					if mode == "cancel-start" {
						_, _ = io.Copy(io.Discard, channel)
						return
					}
					_ = request.Reply(true, nil)
					httpRequest, e := http.ReadRequest(bufio.NewReader(channel))
					if e != nil {
						return
					}
					defer httpRequest.Body.Close()
					f.requests.Add(1)
					decoded, e := base64.URLEncoding.DecodeString(httpRequest.Header.Get("X-Registry-Auth"))
					if e != nil || !bytes.Equal(decoded, auth) || httpRequest.Method != "POST" || httpRequest.URL.Path != "/images/create" || httpRequest.Host != "docker" || httpRequest.URL.Query().Get("fromImage") != image || len(httpRequest.URL.Query()) != 1 {
						f.invalid.Store(true)
					}
					clear(decoded)
					f.onceRequest.Do(func() { close(f.requestSeen) })
					if mode == "loss" {
						return
					}
					if mode == "cancel-body" {
						_, _ = io.WriteString(channel, "HTTP/1.1 200 OK\r\nContent-Length: 200\r\n\r\n{\"status\":")
						_, _ = io.Copy(io.Discard, channel)
						return
					}
					if mode == "cancel-header" {
						_, _ = io.Copy(io.Discard, channel)
						return
					}
					body := "{\"status\":\"done\"}\n"
					if mode == "daemon-error" {
						body = "{\"error\":\"private-daemon-diagnostic\"}\n"
					}
					if mode == "oversize" {
						body = "{\"status\":\"" + strings.Repeat("x", SecretOutputLimit) + "\"}\n"
					}
					response := &http.Response{StatusCode: 200, Status: "200 OK", ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Close: true}
					if mode == "headers" {
						response.Header.Set("X-Private-Diagnostic", strings.Repeat("x", 70<<10))
					}
					_, _ = io.WriteString(channel.Stderr(), "private-stderr-never-returned")
					_ = response.Write(channel)
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					return
				}
			}()
		}
	}()
	return f
}
func (f *stdioPullFixture) close() {
	_ = f.listener.Close()
	f.mu.Lock()
	if f.raw != nil {
		_ = f.raw.Close()
	}
	f.mu.Unlock()
}
func (f *stdioPullFixture) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("private pull SSH connection did not close")
	}
}
func TestSecretSSHPullUsesOnlyFixedExecStdioAndOnePrivateHTTPRequest(t *testing.T) {
	auth := []byte(`{"username":"fixture","password":"registry-private-$(literal)"}`)
	defer clear(auth)
	image := "registry.invalid/approved@sha256:" + strings.Repeat("a", 64)
	f := newStdioPullFixture(t, "ok", auth, image)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := (sshDialer{}).PullSecret(ctx, f.address, SSHConfig{User: "fixture", Password: "fixture-only"}, f.key, image, auth); e != nil {
		t.Fatal("fixed exec transport failed", e)
	}
	f.wait(t)
	if f.direct.Load() != 0 || f.env.Load() != 0 || f.sessions.Load() != 1 || f.requests.Load() != 1 || f.invalid.Load() {
		t.Fatal("private request/argv/host/auth boundary failed", f.direct.Load(), f.env.Load(), f.sessions.Load(), f.requests.Load())
	}
}
func TestSecretSSHPullCancellationAndTransportLossNeverFallbackOrRetry(t *testing.T) {
	for _, mode := range []string{"loss", "cancel-start", "cancel-header", "cancel-body", "daemon-error", "oversize", "headers"} {
		t.Run(mode, func(t *testing.T) {
			auth := []byte(`{"username":"fixture","password":"private-auth-only"}`)
			defer clear(auth)
			image := "registry.invalid/approved@sha256:" + strings.Repeat("a", 64)
			f := newStdioPullFixture(t, mode, auth, image)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- (sshDialer{}).PullSecret(ctx, f.address, SSHConfig{User: "fixture", Password: "fixture-only"}, f.key, image, auth)
			}()
			cancelling := strings.HasPrefix(mode, "cancel-")
			if cancelling {
				event := f.requestSeen
				if mode == "cancel-start" {
					event = f.execSeen
				}
				select {
				case <-event:
				case <-time.After(time.Second):
					t.Fatal("private request did not reach cancel window")
				}
				cancel()
			}
			select {
			case e := <-result:
				if e == nil || strings.Contains(e.Error(), "private-") {
					t.Fatal("failure lost closed diagnostic boundary")
				}
				if cancelling && !errors.Is(e, context.Canceled) {
					t.Fatal("context cancellation not preserved", e)
				}
				if cancelling && !errors.Is(e, ErrOutcomeUnknown) {
					t.Fatal("canceled in-flight private request lost unknown boundary")
				}
				if mode == "oversize" && !errors.Is(e, ErrSecretLimit) {
					t.Fatal("body size bound was not enforced", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("private pull did not stop")
			}
			f.wait(t)
			if f.direct.Load() != 0 || f.env.Load() != 0 || f.sessions.Load() != 1 || f.requests.Load() > 1 || f.invalid.Load() {
				t.Fatal("private pull retried or expanded transport", mode, strconv.Itoa(int(f.requests.Load())))
			}
		})
	}
}
func TestSecretSSHPullWrongHostNeverOpensExecOrSendsRegistryAuth(t *testing.T) {
	auth := []byte(`{"password":"never-send"}`)
	defer clear(auth)
	image := "registry.invalid/approved@sha256:" + strings.Repeat("a", 64)
	f := newStdioPullFixture(t, "ok", auth, image)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e := (sshDialer{}).PullSecret(ctx, f.address, SSHConfig{User: "fixture", Password: "fixture-only"}, "SHA256:"+strings.Repeat("A", 43), image, auth)
	if e == nil || !IsNotStarted(e) {
		t.Fatal("wrong host not rejected before execution", e)
	}
	f.wait(t)
	if f.auth.Load() != 0 || f.sessions.Load() != 0 || f.requests.Load() != 0 {
		t.Fatal("host pin was checked after credential or command")
	}
}

func TestSecretSSHPullInputLimitsStopBeforeSSHContact(t *testing.T) {
	for _, scenario := range []string{"auth", "image", "empty", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			auth := []byte(`{"password":"never-send"}`)
			image := "registry.invalid/approved@sha256:" + strings.Repeat("a", 64)
			f := newStdioPullFixture(t, "ok", auth, image)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "auth":
				auth = bytes.Repeat([]byte("x"), 16385)
			case "image":
				image = strings.Repeat("x", 513)
			case "empty":
				image = ""
			case "canceled":
				cancel()
			}
			defer clear(auth)
			e := (sshDialer{}).PullSecret(ctx, f.address, SSHConfig{User: "fixture", Password: "fixture-only"}, f.key, image, auth)
			if e == nil || !IsNotStarted(e) {
				t.Fatal("invalid request did not stop before execution", e)
			}
			f.close()
			f.wait(t)
			if f.auth.Load() != 0 || f.sessions.Load() != 0 || f.requests.Load() != 0 {
				t.Fatal("invalid pull contacted SSH")
			}
		})
	}
}
