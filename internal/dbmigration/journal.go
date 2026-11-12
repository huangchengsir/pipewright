package dbmigration

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"golang.org/x/crypto/nacl/secretbox"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var ErrJournal = errors.New("dbmigration: local control state unavailable or invalid; business startup blocked")
var ErrLocked = errors.New("dbmigration: another instance process holds the database control lock")
var ErrPersistenceUnknown = errors.New("dbmigration: local persistence outcome unknown; reopen and inspect control state")

const maxControlBytes = 4 << 20

// Control is a process-lifetime lock. Normal runtime and the dedicated migration
// process must both acquire it BEFORE opening any business database. Close is for
// tests/embedders; production keeps the descriptor open until process exit/exec.
type Control struct {
	mu                       sync.Mutex
	directory, lockID, owner string
	file                     *os.File
	key                      *[32]byte
	closed                   bool
}

// AcquireControl touches only a dedicated child of the configured persistent
// DataDir. A failed lock, altered file identity or corrupt ciphertext fails closed.
func AcquireControl(dataDir string, key *[32]byte) (*Control, error) {
	if dataDir == "" {
		return nil, ErrJournal
	}
	parent, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, ErrJournal
	}
	if err = os.MkdirAll(parent, 0700); err != nil {
		return nil, ErrJournal
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, ErrJournal
	}
	directory := filepath.Join(parent, "database-migration")
	err = os.Mkdir(directory, 0700)
	created := err == nil
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, ErrJournal
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrJournal
	}
	if err = secureDirectory(directory, created); err != nil {
		return nil, ErrJournal
	}
	lockPath := filepath.Join(directory, "runtime.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		info, err = os.Lstat(lockPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, ErrJournal
		}
		f, err = os.OpenFile(lockPath, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, ErrJournal
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	if err = lockFile(f); err != nil {
		return nil, ErrLocked
	}
	actual, err := f.Stat()
	if err != nil {
		return nil, ErrJournal
	}
	info, err = os.Lstat(lockPath)
	if err != nil || !os.SameFile(actual, info) || !info.Mode().IsRegular() {
		return nil, ErrJournal
	}
	if checkPrivate(lockPath) != nil {
		return nil, ErrJournal
	}
	id, err := fileIdentity(f)
	if err != nil {
		return nil, ErrJournal
	}
	c := &Control{directory: directory, lockID: id, owner: uuid.NewString(), file: f}
	if key != nil {
		// Domain separation prevents interpreting a vault ciphertext as control data.
		mac := hmac.New(sha256.New, key[:])
		mac.Write([]byte("pipewright/database-migration/control/v1"))
		c.key = new([32]byte)
		copy(c.key[:], mac.Sum(nil))
	}
	for _, name := range []string{"state", "active"} {
		if _, err = c.read(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	ok = true
	return c, nil
}
func (c *Control) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	unlockFile(c.file)
	return c.file.Close()
}

type envelope struct {
	Version   int             `json:"version"`
	Purpose   string          `json:"purpose"`
	Directory string          `json:"directory"`
	LockID    string          `json:"lockId"`
	Payload   json.RawMessage `json:"payload"`
}

func (c *Control) intact() bool {
	if c.closed {
		return false
	}
	info, err := os.Lstat(filepath.Join(c.directory, "runtime.lock"))
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	actual, err := c.file.Stat()
	return err == nil && os.SameFile(actual, info) && checkPrivate(c.directory) == nil
}
func (c *Control) read(name string) (json.RawMessage, error) {
	if !c.intact() {
		return nil, ErrJournal
	}
	path := filepath.Join(c.directory, name+".sealed")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxControlBytes || c.key == nil || checkPrivate(path) != nil {
		return nil, ErrJournal
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrJournal
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, ErrJournal
	}
	data, err := io.ReadAll(io.LimitReader(f, maxControlBytes+1))
	if err != nil || len(data) < 24+secretbox.Overhead || len(data) > maxControlBytes {
		return nil, ErrJournal
	}
	var nonce [24]byte
	copy(nonce[:], data[:24])
	plain, ok := secretbox.Open(nil, data[24:], &nonce, c.key)
	if !ok {
		return nil, ErrJournal
	}
	defer clear(plain)
	var e envelope
	if json.Unmarshal(plain, &e) != nil || e.Version != 1 || e.Purpose != name || e.Directory != c.directory || e.LockID != c.lockID {
		return nil, ErrJournal
	}
	return e.Payload, nil
}
func (c *Control) write(name string, value any) error {
	if c.key == nil || !c.intact() {
		return ErrJournal
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return ErrJournal
	}
	defer clear(payload)
	plain, err := json.Marshal(envelope{Version: 1, Purpose: name, Directory: c.directory, LockID: c.lockID, Payload: payload})
	if err != nil {
		return ErrJournal
	}
	defer clear(plain)
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return ErrJournal
	}
	sealed := secretbox.Seal(nonce[:], plain, &nonce, c.key)
	if len(sealed) > maxControlBytes {
		return ErrJournal
	}
	f, err := os.CreateTemp(c.directory, ".control-*")
	if err != nil {
		return ErrJournal
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(sealed)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrJournal
	}
	if checkPrivate(temp) != nil {
		return ErrJournal
	}
	if err = replaceFile(temp, filepath.Join(c.directory, name+".sealed")); err != nil {
		return ErrPersistenceUnknown
	}
	if syncDirectory(c.directory) != nil {
		return ErrPersistenceUnknown
	}
	return nil
}
func decodeStrict(b []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return ErrJournal
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrJournal
	}
	return nil
}
