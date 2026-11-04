// Package kubernetesowner proves same-host process departure using a held OS lock.
// Database age or a different instance's owner is never evidence of departure.
package kubernetesowner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrLocked = errors.New("kubernetes owner already active")
var ErrJournal = errors.New("kubernetes owner journal unavailable or identity mismatch")

const maxJournal = 128 << 10
const lockName = "kubernetes-controller.lock"
const journalName = "kubernetes-controller.json"

type journal struct {
	Schema       int      `json:"schema"`
	Directory    string   `json:"directory"`
	LockIdentity string   `json:"lockIdentity"`
	Nonce        string   `json:"nonce"`
	Owners       []string `json:"owners"`
}

// Guard must remain reachable and open until all old controller code is gone.
// Production holds it to process exit/exec; Close exists for controlled embedders/tests.
type Guard struct {
	file     *os.File
	journal  journal
	owner    string
	departed []string
	once     sync.Once
	err      error
}

func (g *Guard) Owner() string            { return g.owner }
func (g *Guard) DepartedOwners() []string { return append([]string(nil), g.departed...) }
func (g *Guard) Close() error             { g.once.Do(func() { g.err = g.file.Close() }); return g.err }
func boundedFile(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return nil, ErrJournal
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, ErrJournal
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil || !os.SameFile(info, stat) {
		return nil, ErrJournal
	}
	b, e := io.ReadAll(io.LimitReader(f, maxJournal+1))
	if e != nil || len(b) > maxJournal {
		return nil, ErrJournal
	}
	return b, nil
}

// Acquire creates a private state directory, takes an exclusive nonblocking OS
// lock, checks canonical path + OS file identity + lock nonce, then durably records
// the new owner BEFORE allowing work. A lost/corrupt journal fails closed.
func Acquire(directory string) (*Guard, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, ErrJournal
	}
	abs, e := filepath.Abs(directory)
	if e != nil {
		return nil, ErrJournal
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return nil, ErrJournal
	}
	canonical, e := filepath.EvalSymlinks(abs)
	if e != nil {
		return nil, ErrJournal
	}
	canonical = filepath.Clean(canonical)
	lp := filepath.Join(canonical, lockName)
	jp := filepath.Join(canonical, journalName)
	f, e := os.OpenFile(lp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	created := e == nil
	if errors.Is(e, os.ErrExist) {
		info, err := os.Lstat(lp)
		if err != nil || !info.Mode().IsRegular() {
			return nil, ErrJournal
		}
		f, e = os.OpenFile(lp, os.O_RDWR, 0)
	}
	if e != nil {
		return nil, ErrJournal
	}
	success := false
	defer func() {
		if !success {
			f.Close()
		}
	}()
	if e = lockFile(f); e != nil {
		return nil, e
	}
	info, e := os.Lstat(lp)
	if e != nil || !info.Mode().IsRegular() {
		return nil, ErrJournal
	}
	actual, e := f.Stat()
	if e != nil || !os.SameFile(info, actual) {
		return nil, ErrJournal
	}
	if e = checkPermissions(canonical, f); e != nil {
		return nil, e
	}
	identity, e := fileIdentity(f)
	if e != nil {
		return nil, ErrJournal
	}
	var j journal
	if created {
		if _, e = os.Lstat(jp); !errors.Is(e, os.ErrNotExist) {
			return nil, ErrJournal
		}
		j = journal{Schema: 1, Directory: canonical, LockIdentity: identity, Nonce: uuid.NewString(), Owners: []string{}}
		if _, e = f.WriteString(j.Nonce); e != nil {
			return nil, ErrJournal
		}
		if e = f.Sync(); e != nil {
			return nil, ErrJournal
		}
	} else {
		nonce, e := io.ReadAll(io.LimitReader(f, 65))
		if e != nil || len(nonce) != 36 {
			return nil, ErrJournal
		}
		raw, e := boundedFile(jp)
		if e != nil {
			return nil, e
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&j) != nil || decoder.Decode(new(any)) != io.EOF || j.Schema != 1 || j.Directory != canonical || j.LockIdentity != identity || j.Nonce != string(nonce) || len(j.Owners) == 0 || len(j.Owners) >= 1024 {
			return nil, ErrJournal
		}
		// Journal is generated locally. Canonical encoding also rejects duplicate keys.
		encoded, _ := json.Marshal(j)
		if !bytes.Equal(raw, encoded) {
			return nil, ErrJournal
		}
		seen := map[string]bool{}
		for _, o := range j.Owners {
			if !strings.HasPrefix(o, "process_") || len(o) != 44 || seen[o] {
				return nil, ErrJournal
			}
			if _, e = uuid.Parse(strings.TrimPrefix(o, "process_")); e != nil {
				return nil, ErrJournal
			}
			seen[o] = true
		}
	}
	g := &Guard{file: f, owner: "process_" + uuid.NewString(), departed: append([]string(nil), j.Owners...)}
	j.Owners = append(j.Owners, g.owner)
	if e = writeJournal(canonical, jp, j); e != nil {
		return nil, e
	}
	g.journal = j
	success = true
	return g, nil
}

// PruneDeparted removes only supplied owners whose lack of unresolved operations,
// holds and leases has been confirmed by the domain. Unknown/current owners are
// retained. Failed proof or persistence must leave the prior journal unchanged.
func (g *Guard) PruneDeparted(confirmed []string) error {
	allowed := map[string]bool{}
	for _, o := range confirmed {
		allowed[o] = true
	}
	j := g.journal
	j.Owners = make([]string, 0, len(g.journal.Owners))
	for _, o := range g.journal.Owners {
		if o == g.owner || !allowed[o] {
			j.Owners = append(j.Owners, o)
		}
	}
	if len(j.Owners) == len(g.journal.Owners) {
		return nil
	}
	// Detect lock replacement or journal tampering even while this descriptor stays held.
	current, e := os.Stat(filepath.Join(j.Directory, lockName))
	if e != nil {
		return ErrJournal
	}
	held, e := g.file.Stat()
	if e != nil || !os.SameFile(current, held) {
		return fmt.Errorf("%w: lock_identity_changed: %v", ErrJournal, e)
	}
	jp := filepath.Join(j.Directory, journalName)
	raw, e := boundedFile(jp)
	if e != nil {
		return e
	}
	expected, _ := json.Marshal(g.journal)
	if !bytes.Equal(raw, expected) {
		return fmt.Errorf("%w: unexpected_journal_content", ErrJournal)
	}
	if e = writeJournal(j.Directory, jp, j); e != nil {
		return e
	}
	g.journal = j
	g.departed = nil
	for _, o := range j.Owners {
		if o != g.owner {
			g.departed = append(g.departed, o)
		}
	}
	return nil
}
func writeJournal(directory, path string, j journal) error {
	raw, e := json.Marshal(j)
	if e != nil || len(raw) > maxJournal {
		return ErrJournal
	}
	tmp, e := os.CreateTemp(directory, ".kubernetes-controller-*.tmp")
	if e != nil {
		return ErrJournal
	}
	temp := tmp.Name()
	defer os.Remove(temp)
	if _, e = tmp.Write(raw); e == nil {
		e = tmp.Sync()
	}
	ce := tmp.Close()
	if e != nil || ce != nil {
		return fmt.Errorf("%w: file_sync: %v", ErrJournal, errors.Join(e, ce))
	}
	if e = replaceJournal(temp, path); e != nil {
		return fmt.Errorf("%w: replace: %v", ErrJournal, e)
	}
	if e = syncDirectory(directory); e != nil {
		return fmt.Errorf("%w: directory_sync: %v", ErrJournal, e)
	}
	return nil
}
