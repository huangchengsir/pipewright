package project

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/huangchengsir/pipewright/internal/gitauth"
	"golang.org/x/crypto/ssh"
)

func TestProberSSHWithPinnedHostKey(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for the SSH Git fixture")
	}
	root := t.TempDir()
	work, bare := filepath.Join(root, "work"), filepath.Join(root, "bare.git")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git(work, "init", "-q", "-b", "main")
	git(work, "commit", "-q", "--allow-empty", "-m", "fixture")
	git(root, "clone", "-q", "--bare", work, bare)

	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	clientPKCS8, err := x509.MarshalPKCS8PrivateKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	clientPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientPKCS8}))
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
			return nil, errors.New("unauthorized test key")
		}
		return nil, nil
	}}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, chans, reqs, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						_ = ch.Reject(ssh.UnknownChannelType, "session required")
						continue
					}
					channel, requests, err := ch.Accept()
					if err != nil {
						continue
					}
					for request := range requests {
						if request.Type != "exec" {
							_ = request.Reply(false, nil)
							continue
						}
						var payload struct{ Command string }
						if ssh.Unmarshal(request.Payload, &payload) != nil || len(payload.Command) < len("git-upload-pack") || payload.Command[:len("git-upload-pack")] != "git-upload-pack" {
							_ = request.Reply(false, nil)
							continue
						}
						_ = request.Reply(true, nil)
						cmd := exec.Command(gitBin, "upload-pack", bare)
						cmd.Stdin, cmd.Stdout, cmd.Stderr = channel, channel, channel.Stderr()
						status := uint32(0)
						if cmd.Run() != nil {
							status = 1
						}
						_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						_ = channel.Close()
						break
					}
				}
			}()
		}
	}()

	knownHosts := filepath.Join(root, "known_hosts")
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	knownHostID := fmt.Sprintf("[%s]:%s", host, port)
	line := fmt.Sprintf("%s %s", knownHostID, ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))
	if err := os.WriteFile(knownHosts, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)
	addr := fmt.Sprintf("ssh://git@%s/repo.git", listener.Addr())
	prober := goGitProber{allowInsecureSchemes: true}
	branch, err := prober.Probe(context.Background(), addr, "", clientPEM)
	if err != nil || branch != "main" {
		t.Fatalf("SSH probe: branch=%q err=%v", branch, err)
	}
	auth, err := gitauth.AuthMethod(addr, "", clientPEM)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := gogit.PlainCloneContext(context.Background(), filepath.Join(root, "checkout"), false, &gogit.CloneOptions{URL: addr, Auth: auth})
	if err != nil {
		t.Fatalf("SSH clone: %v", err)
	}
	if head, err := repo.Head(); err != nil || head.Name().Short() != "main" {
		t.Fatalf("SSH clone HEAD: %v, %v", head, err)
	}

	_, wrongPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongSigner, err := ssh.NewSignerFromKey(wrongPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHosts, []byte(fmt.Sprintf("%s %s", knownHostID, ssh.MarshalAuthorizedKey(wrongSigner.PublicKey()))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prober.Probe(context.Background(), addr, "", clientPEM); !errors.Is(err, ErrRepoUnreachable) {
		t.Fatalf("wrong host key should be rejected: %v", err)
	}
}

func TestProberRealSSHIntegration(t *testing.T) {
	addr := os.Getenv("PIPEWRIGHT_TEST_SSH_REPO")
	keyFile := os.Getenv("PIPEWRIGHT_TEST_SSH_KEY_FILE")
	knownHosts := os.Getenv("PIPEWRIGHT_TEST_SSH_KNOWN_HOSTS")
	if addr == "" || keyFile == "" || knownHosts == "" {
		t.Skip("set PIPEWRIGHT_TEST_SSH_REPO, PIPEWRIGHT_TEST_SSH_KEY_FILE, and PIPEWRIGHT_TEST_SSH_KNOWN_HOSTS to run")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)
	auth, err := gitauth.AuthMethod(addr, "", string(key))
	if err != nil {
		t.Fatalf("SSH auth: %v", err)
	}
	repo, err := gogit.PlainCloneContext(context.Background(), filepath.Join(t.TempDir(), "checkout"), false, &gogit.CloneOptions{URL: addr, Auth: auth, Depth: 1})
	if err != nil {
		t.Fatalf("SSH clone: %v", err)
	}
	branch, err := (goGitProber{}).Probe(context.Background(), addr, "", string(key))
	if err != nil {
		t.Fatalf("SSH probe: %v", err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if branch != head.Name().Short() {
		t.Fatalf("probe branch %q does not match clone branch %q", branch, head.Name().Short())
	}
	t.Logf("SSH probe and clone succeeded: branch=%s commit=%s", branch, head.Hash())
}
