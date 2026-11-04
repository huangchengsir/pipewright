package target

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func privateTestSSH(t *testing.T, respond func(ssh.Channel, <-chan *ssh.Request)) (string, string, <-chan struct{}) {
	t.Helper()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(key)
	if e != nil {
		t.Fatal(e)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
		if string(pass) != "fixture-only" {
			return nil, errors.New("denied")
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		server, chans, reqs, e := ssh.NewServerConn(conn, cfg)
		if e != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(reqs)
		for next := range chans {
			if next.ChannelType() != "session" {
				_ = next.Reject(ssh.UnknownChannelType, "no")
				continue
			}
			ch, requests, e := next.Accept()
			if e != nil {
				return
			}
			respond(ch, requests)
			ch.Close()
			_ = server.Wait()
			return
		}
	}()
	return listener.Addr().String(), ssh.FingerprintSHA256(signer.PublicKey()), done
}
func TestSecretSSHHostKeyAndPrivateStdin(t *testing.T) {
	received := make(chan string, 1)
	marker := "stdin-only-fixture-secret"
	literal := "$(echo injected);`x`'"
	addr, key, done := privateTestSSH(t, func(ch ssh.Channel, requests <-chan *ssh.Request) {
		defer ch.Close()
		for req := range requests {
			if req.Type != "exec" {
				_ = req.Reply(false, nil)
				continue
			}
			var command struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &command)
			_ = req.Reply(true, nil)
			in, _ := io.ReadAll(ch)
			received <- command.Command + "\n" + string(in)
			_, _ = ch.Write(bytes.Repeat([]byte("o"), SecretOutputLimit+40))
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			return
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, e := (sshDialer{}).RunSecret(ctx, addr, SSHConfig{User: "fixture", Password: "fixture-only"}, key, []string{"docker", "exec", "-i", "cid", literal}, []byte(marker))
	defer func() {
		if out != nil {
			clear(out.Stdout)
			clear(out.Stderr)
		}
	}()
	if e != nil || out == nil || !out.Truncated || len(out.Stdout)+len(out.Stderr) > SecretOutputLimit {
		t.Fatal("transport", e)
	}
	got := <-received
	parts := strings.SplitN(got, "\n", 2)
	if strings.Contains(parts[0], marker) || parts[1] != marker || !strings.Contains(parts[0], shellQuote(literal)) {
		t.Fatal("stdin/argv separation")
	}
	<-done
}
func TestSecretSSHWrongHostNeverSendsCredentialInput(t *testing.T) {
	reached := false
	addr, _, done := privateTestSSH(t, func(ssh.Channel, <-chan *ssh.Request) { reached = true })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	key := "SHA256:" + strings.Repeat("A", 43)
	out, e := (sshDialer{}).RunSecret(ctx, addr, SSHConfig{User: "fixture", Password: "fixture-only"}, key, []string{"docker", "info"}, []byte("never-send"))
	if e == nil || out != nil || !IsNotStarted(e) {
		t.Fatal("host mismatch not rejected", e)
	}
	<-done
	if reached {
		t.Fatal("wrong host reached command")
	}
}
func TestSecretSSHCancelDrainsAndReturnsUnknown(t *testing.T) {
	started := make(chan struct{})
	addr, key, done := privateTestSSH(t, func(ch ssh.Channel, requests <-chan *ssh.Request) {
		for req := range requests {
			if req.Type == "exec" {
				_ = req.Reply(true, nil)
				close(started)
				_, _ = io.Copy(io.Discard, ch)
				return
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, e := (sshDialer{}).RunSecret(ctx, addr, SSHConfig{User: "fixture", Password: "fixture-only"}, key, []string{"docker", "exec", "cid", "sleep", "100"}, nil)
		result <- e
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("not started")
	}
	cancel()
	select {
	case e := <-result:
		if !errors.Is(e, context.Canceled) || !errors.Is(e, ErrOutcomeUnknown) {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not cancel")
	}
	<-done
}

type pullRoundTripper func(*http.Request) (*http.Response, error)

func (f pullRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSecretPullFixedEndpointAndNoRawErrors(t *testing.T) {
	for _, body := range []string{"{\"status\":\"done\"}\n", "{\"error\":\"sensitive-value\"}\n"} {
		rt := pullRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Method != "POST" || r.URL.Host != "docker" || r.URL.Path != "/images/create" || r.Header.Get("X-Registry-Auth") == "" {
				t.Fatal("pull request")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		auth, _ := json.Marshal(map[string]string{"username": "fixture", "password": "private-fixture"})
		e := PullDockerImage(context.Background(), rt, "registry.invalid/repo@sha256:"+strings.Repeat("a", 64), auth)
		clear(auth)
		if strings.Contains(body, "error") {
			if e == nil || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("raw error escaped")
			}
		} else if e != nil {
			t.Fatal(e)
		}
	}
}
