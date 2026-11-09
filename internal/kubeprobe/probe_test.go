package kubeprobe

import (
	"context"
	"encoding/binary"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPProbeRejectsRedirectErrorAndMismatchWithoutReturningBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready":
			w.Write([]byte("version-v2"))
		case "/redirect":
			http.Redirect(w, r, "/ready", 302)
		default:
			w.WriteHeader(503)
			w.Write([]byte("private-failure-detail"))
		}
	}))
	defer server.Close()
	for _, item := range []struct {
		path, expected string
		ready          bool
	}{{"/ready", "version-v2", true}, {"/ready", "foreign", false}, {"/redirect", "version-v2", false}, {"/failed", "", false}} {
		e := Run(context.Background(), Spec{Kind: "http", Target: server.URL + item.path, Expected: item.expected, TimeoutSeconds: 2})
		if (e == nil) != item.ready {
			t.Fatal(item.path, e)
		}
	}
}
func TestDNSProbeActuallyChecksUDPAndTCPAnswers(t *testing.T) {
	for _, protocol := range []string{"udp", "tcp"} {
		t.Run(protocol, func(t *testing.T) {
			response := func(raw []byte) []byte {
				var question dnsmessage.Message
				if question.Unpack(raw) != nil {
					return nil
				}
				answer := dnsmessage.Message{Header: dnsmessage.Header{ID: question.ID, Response: true, RecursionAvailable: true}, Questions: question.Questions, Answers: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 5}, Body: &dnsmessage.AResource{A: [4]byte{10, 20, 30, 40}}}}}
				body, _ := answer.Pack()
				return body
			}
			address := ""
			if protocol == "udp" {
				listener, e := net.ListenPacket("udp", "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				defer listener.Close()
				address = listener.LocalAddr().String()
				go func() {
					for {
						buffer := make([]byte, 4096)
						n, peer, e := listener.ReadFrom(buffer)
						if e != nil {
							return
						}
						listener.WriteTo(response(buffer[:n]), peer)
					}
				}()
			} else {
				listener, e := net.Listen("tcp", "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				defer listener.Close()
				address = listener.Addr().String()
				go func() {
					for {
						connection, e := listener.Accept()
						if e != nil {
							return
						}
						go func() {
							defer connection.Close()
							connection.SetDeadline(time.Now().Add(time.Second))
							var size [2]byte
							if _, e := io.ReadFull(connection, size[:]); e != nil {
								return
							}
							body := make([]byte, binary.BigEndian.Uint16(size[:]))
							if _, e := io.ReadFull(connection, body); e != nil {
								return
							}
							body = response(body)
							binary.BigEndian.PutUint16(size[:], uint16(len(body)))
							connection.Write(append(size[:], body...))
						}()
					}
				}()
			}
			for _, expected := range []string{"10.20.30.40", "10.20.30.41"} {
				e := Run(context.Background(), Spec{Kind: "dns", Target: "service.app.svc.cluster.local", Server: address, Protocol: protocol, Expected: expected, TimeoutSeconds: 2})
				if (e == nil) != (expected == "10.20.30.40") {
					t.Fatal("wrong DNS answer accepted", expected, e)
				}
			}
		})
	}
}
func TestNetworkDenyRejectsDNSAndMalformedTarget(t *testing.T) {
	for _, target := range []string{"does-not-resolve.invalid:53", "127.0.0.1", ":80", "127.0.0.1:"} {
		if e := Run(context.Background(), Spec{Kind: "network-deny", Target: target, TimeoutSeconds: 1}); e == nil {
			t.Fatal("unavailable target mistaken for policy enforcement", target)
		}
	}
}
