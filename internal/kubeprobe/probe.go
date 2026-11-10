// Package kubeprobe runs fixed, credential-free checks inside approved probe
// Jobs. It never executes scripts or emits remote response/error content.
package kubeprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrProbe = errors.New("kubernetes_probe_failed")

type Spec struct {
	WaitSeconds    int    `json:"waitSeconds,omitempty"`
	Kind           string `json:"kind"`
	Target         string `json:"target"`
	Expected       string `json:"expected,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
	Server         string `json:"server,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

func Run(ctx context.Context, spec Spec) error {
	if spec.WaitSeconds < 0 || spec.WaitSeconds > 600 {
		return ErrProbe
	}
	if spec.WaitSeconds == 0 {
		return runOnce(ctx, spec)
	}
	if spec.Kind == "storage" || spec.Kind == "network-deny" {
		return ErrProbe
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(spec.WaitSeconds)*time.Second)
	defer cancel()
	for {
		if runOnce(work, spec) == nil {
			return nil
		}
		select {
		case <-work.Done():
			return ErrProbe
		case <-time.After(time.Second):
		}
	}
}
func runOnce(ctx context.Context, spec Spec) error {
	if spec.TimeoutSeconds < 1 || spec.TimeoutSeconds > 30 || len(spec.Target) == 0 || len(spec.Target) > 2048 || len(spec.Expected) > 4096 {
		return ErrProbe
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
	defer cancel()
	switch spec.Kind {
	case "dns":
		return dns(work, spec)
	case "http":
		target, e := url.Parse(spec.Target)
		if e != nil || target.User != nil || target.Host == "" || target.Fragment != "" || target.Scheme != "http" && target.Scheme != "https" {
			return ErrProbe
		}
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, e := http.NewRequestWithContext(work, "GET", target.String(), nil)
		if e != nil {
			return ErrProbe
		}
		response, e := client.Do(request)
		if e != nil {
			return ErrProbe
		}
		defer response.Body.Close()
		body, e := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
		defer clear(body)
		if e != nil || len(body) > 64<<10 || response.StatusCode < 200 || response.StatusCode >= 300 || spec.Expected != "" && !bytes.Contains(body, []byte(spec.Expected)) {
			return ErrProbe
		}
		return nil
	case "tcp", "network-allow", "network-deny":
		host, port, e := net.SplitHostPort(spec.Target)
		if e != nil || host == "" || port == "" {
			return ErrProbe
		}
		if spec.Kind == "network-deny" && net.ParseIP(host) == nil {
			return ErrProbe
		}
		dialer := &net.Dialer{}
		connection, e := dialer.DialContext(work, "tcp", spec.Target)
		if e == nil {
			connection.Close()
			if spec.Kind == "network-deny" {
				return ErrProbe
			}
			return nil
		}
		if spec.Kind == "network-deny" && deniedTCPConnection(ctx, work, e) {
			return nil
		}
		return ErrProbe
	case "storage":
		return storage(spec)
	}
	return ErrProbe
}
func dns(ctx context.Context, spec Spec) error {
	if spec.Protocol != "udp" && spec.Protocol != "tcp" {
		return ErrProbe
	}
	host, port, e := net.SplitHostPort(spec.Server)
	if e != nil || net.ParseIP(host) == nil || port == "" {
		return ErrProbe
	}
	expected := net.ParseIP(spec.Expected)
	if expected == nil {
		return ErrProbe
	}
	name, e := dnsmessage.NewName(strings.TrimSuffix(spec.Target, ".") + ".")
	if e != nil {
		return ErrProbe
	}
	var random [2]byte
	if _, e = rand.Read(random[:]); e != nil {
		return ErrProbe
	}
	id := binary.BigEndian.Uint16(random[:])
	questionType := dnsmessage.TypeA
	if expected.To4() == nil {
		questionType = dnsmessage.TypeAAAA
	}
	message := dnsmessage.Message{Header: dnsmessage.Header{ID: id, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: questionType, Class: dnsmessage.ClassINET}}}
	request, e := message.Pack()
	if e != nil {
		return ErrProbe
	}
	connection, e := (&net.Dialer{}).DialContext(ctx, spec.Protocol, spec.Server)
	if e != nil {
		return ErrProbe
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		connection.SetDeadline(deadline)
	}
	var body []byte
	if spec.Protocol == "tcp" {
		frame := make([]byte, len(request)+2)
		binary.BigEndian.PutUint16(frame, uint16(len(request)))
		copy(frame[2:], request)
		if n, e := connection.Write(frame); e != nil || n != len(frame) {
			return ErrProbe
		}
		var size [2]byte
		if _, e = io.ReadFull(connection, size[:]); e != nil {
			return ErrProbe
		}
		count := int(binary.BigEndian.Uint16(size[:]))
		if count < 12 || count > 65535 {
			return ErrProbe
		}
		body = make([]byte, count)
		if _, e = io.ReadFull(connection, body); e != nil {
			return ErrProbe
		}
	} else {
		if n, e := connection.Write(request); e != nil || n != len(request) {
			return ErrProbe
		}
		body = make([]byte, 4096)
		count, e := connection.Read(body)
		if e != nil {
			return ErrProbe
		}
		body = body[:count]
	}
	var response dnsmessage.Message
	if response.Unpack(body) != nil || response.ID != id || !response.Response || response.Truncated || response.RCode != dnsmessage.RCodeSuccess || len(response.Answers) > 128 || len(response.Questions) != 1 || response.Questions[0].Name != name || response.Questions[0].Type != questionType {
		return ErrProbe
	}
	names := map[string]bool{strings.ToLower(name.String()): true}
	for step := 0; step < 8; step++ {
		for _, answer := range response.Answers {
			if cname, ok := answer.Body.(*dnsmessage.CNAMEResource); ok && names[strings.ToLower(answer.Header.Name.String())] {
				names[strings.ToLower(cname.CNAME.String())] = true
			}
		}
	}
	for _, answer := range response.Answers {
		if address, ok := answer.Body.(*dnsmessage.AAAAResource); ok && questionType == dnsmessage.TypeAAAA && names[strings.ToLower(answer.Header.Name.String())] && net.IP(address.AAAA[:]).Equal(expected) {
			return nil
		}
		if address, ok := answer.Body.(*dnsmessage.AResource); ok && names[strings.ToLower(answer.Header.Name.String())] && net.IP(address.A[:]).Equal(expected) {
			return nil
		}
	}
	return ErrProbe
}
func storage(spec Spec) error {
	if filepath.Clean(spec.Target) != spec.Target || !strings.HasPrefix(spec.Target, "/data/pw-check-") || strings.Contains(spec.Target[len("/data/"):], "/") || len(spec.Expected) < 16 || len(spec.Expected) > 128 {
		return ErrProbe
	}
	file, e := os.OpenFile(spec.Target, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return ErrProbe
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return ErrProbe
	}
	if _, e = file.WriteString(spec.Expected); e != nil {
		return ErrProbe
	}
	if e = file.Sync(); e != nil {
		return ErrProbe
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return ErrProbe
	}
	body, e := io.ReadAll(io.LimitReader(file, 129))
	if e != nil || string(body) != spec.Expected {
		return ErrProbe
	}
	current, e := os.Lstat(spec.Target)
	if e != nil || !os.SameFile(info, current) || current.Mode()&os.ModeSymlink != 0 {
		return ErrProbe
	}
	if e = os.Remove(spec.Target); e != nil {
		return ErrProbe
	}
	return nil
}
