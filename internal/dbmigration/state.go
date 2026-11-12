package dbmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"os"
	"reflect"
)

type Phase string

const (
	Prepared         Phase = "prepared"
	Quiescing        Phase = "quiescing"
	Frozen           Phase = "frozen"
	Copying          Phase = "copying"
	Verified         Phase = "verified"
	ActivationIntent Phase = "activation_intent"
	AwaitingRestart  Phase = "awaiting_restart"
	Completed        Phase = "completed"
	Aborted          Phase = "aborted"
)

// Connection and State are LOCAL encrypted control records, never HTTP DTOs.
// Their DSNs must not be logged or returned to clients.
type Connection struct {
	Driver string `json:"driver"`
	DSN    string `json:"dsn"`
}
type RuntimePaths struct {
	DataDir      string `json:"dataDir"`
	Artifacts    string `json:"artifacts"`
	Repositories string `json:"repositories"`
	Cache        string `json:"cache"`
}
type ActiveConfig struct {
	Managed    bool         `json:"managed"`
	Generation uint64       `json:"generation"`
	TaskID     string       `json:"taskId"`
	Connection Connection   `json:"connection"`
	Endpoint   Endpoint     `json:"endpoint"`
	Paths      RuntimePaths `json:"paths"`
}
type State struct {
	TargetPermissions string       `json:"targetPermissions,omitempty"`
	Progress          []TableProof `json:"progress,omitempty"`
	Version           int          `json:"version"`
	Revision          uint64       `json:"revision"`
	TaskID            string       `json:"taskId"`
	Phase             Phase        `json:"phase"`
	Owner             string       `json:"owner"`
	Source            ActiveConfig `json:"source"`
	Target            ActiveConfig `json:"target"`
	Manifest          string       `json:"manifest"`
	Report            *CopyReport  `json:"report,omitempty"`
}

func (c *Control) State() (*State, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.state() }
func (c *Control) state() (*State, error) {
	raw, err := c.read("state")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var s State
	if decodeStrict(raw, &s) != nil || s.Version != 1 || s.Revision == 0 || !validStateManifest(s) || !validTask(s.TaskID) || s.Source.Endpoint.Identity == "" || !validPhase(s.Phase) {
		return nil, ErrJournal
	}
	return &s, nil
}
func validTask(s string) bool { _, err := uuid.Parse(s); return err == nil }
func validPhase(p Phase) bool {
	switch p {
	case Prepared, Quiescing, Frozen, Copying, Verified, ActivationIntent, AwaitingRestart, Completed, Aborted:
		return true
	}
	return false
}
func profileDigest(p ActiveConfig) string {
	b, _ := json.Marshal(p)
	defer clear(b)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func reportDigest(p *CopyReport) string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validReport(p *CopyReport) bool {
	if p == nil || p.ManifestSHA256 != ManifestDigest() || len(p.Tables) != len(Manifest()) {
		return false
	}
	for i, t := range Manifest() {
		entry := p.Tables[i]
		b, e := hex.DecodeString(entry.SHA256)
		if e != nil || len(b) != 32 || entry.Rows < 0 || entry.Table != t.Name {
			return false
		}
	}
	return true
}

// Plan persists the preflight-bound identities and connection configuration. It
// does not authorize target initialization or stop a running business process.
func (c *Control) Plan(source, target ActiveConfig) (*State, error) {
	return c.plan(source, target, "initialization_required")
}
func (c *Control) plan(source, target ActiveConfig, permissions string) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old, err := c.state()
	if err != nil {
		return nil, err
	}
	if old != nil && old.Phase != Completed && old.Phase != Aborted && old.Phase != Prepared {
		return nil, ErrInvalidState
	}
	if !validTask(source.Endpoint.Identity) || target.Endpoint.Identity != "" || SameEndpoint(source.Endpoint, target.Endpoint) || source.Generation == ^uint64(0) || source.Connection.Driver != source.Endpoint.Dialect || target.Connection.Driver != target.Endpoint.Dialect || source.Connection.DSN == "" || target.Connection.DSN == "" || !reflect.DeepEqual(source.Paths, target.Paths) {
		return nil, ErrIdentity
	}
	task := uuid.NewString()
	target.Generation = source.Generation + 1
	target.TaskID = task
	s := &State{TargetPermissions: permissions, Version: 1, Revision: 1, TaskID: task, Phase: Prepared, Owner: c.owner, Source: source, Target: target, Manifest: ManifestDigest()}
	if err = c.write("state", s); err != nil {
		return nil, err
	}
	return s, nil
}
func (c *Control) transition(task string, revision uint64, from, to Phase, change func(*State) error) (*State, error) {
	s, err := c.state()
	if err != nil {
		return nil, err
	}
	if s == nil || s.TaskID != task || s.Revision != revision || s.Phase != from {
		return nil, ErrInvalidState
	}
	if change != nil {
		if err = change(s); err != nil {
			return nil, err
		}
	}
	s.Phase = to
	s.Revision++
	if err = c.write("state", s); err != nil {
		return nil, err
	}
	return s, nil
}
func (c *Control) BeginQuiesce(task string, revision uint64) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transition(task, revision, Prepared, Quiescing, func(s *State) error {
		if s.Manifest != ManifestDigest() {
			return ErrInvalidState
		}
		return nil
	})
}

// Takeover requires a DIFFERENT process owner to have acquired the OS lock. An
// HTTP fence, worker cancellation or elapsed timeout alone cannot reach Frozen.
func (c *Control) Takeover(task string, revision uint64) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transition(task, revision, Quiescing, Frozen, func(s *State) error {
		if s.Owner == c.owner {
			return ErrLocked
		}
		s.Owner = c.owner
		return nil
	})
}
func (c *Control) BeginCopy(task string, revision uint64, target Endpoint) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transition(task, revision, Frozen, Copying, func(s *State) error {
		if !validTask(target.Identity) || SameEndpoint(s.Source.Endpoint, target) || !targetLocationMatches(s.Target.Endpoint, target) {
			return ErrIdentity
		}
		s.Target.Endpoint = target
		return nil
	})
}
func (c *Control) RecordVerified(task string, revision uint64, report *CopyReport) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transition(task, revision, Copying, Verified, func(s *State) error {
		if !validReport(report) {
			return ErrMismatch
		}
		s.Report = report
		return nil
	})
}

// StageActivation orders intent -> active config -> awaiting restart. Any failure
// leaves startup closed. Reopening observes the durable state, never retries this
// operation blindly. Environment-only deployments must await external restart.
func (c *Control) StageActivation(task string, revision uint64) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.transition(task, revision, Verified, ActivationIntent, nil)
	if err != nil {
		return nil, err
	}
	if err = c.write("active", s.Target); err != nil {
		return nil, err
	}
	return c.transition(task, s.Revision, ActivationIntent, AwaitingRestart, nil)
}
func (c *Control) Active() (*ActiveConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active()
}
func (c *Control) active() (*ActiveConfig, error) {
	raw, err := c.read("active")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var a ActiveConfig
	if decodeStrict(raw, &a) != nil || (a.Generation == 0 && a.TaskID != "") || (a.Generation > 0 && !validTask(a.TaskID)) || !validTask(a.Endpoint.Identity) {
		return nil, ErrJournal
	}
	return &a, nil
}
func sameLocation(a, b Endpoint) bool {
	a.Identity = ""
	b.Identity = ""
	return reflect.DeepEqual(a, b)
}
func exactEndpoint(a, b Endpoint) bool {
	return a.Identity != "" && a.Identity == b.Identity && sameLocation(a, b)
}

// sameBootProfile excludes only connection credentials/options after an actual
// endpoint lookup. Database identity/location and every control boundary remain
// mandatory. In-progress copy/activation continues to use the full digest.
func sameBootProfile(a, b ActiveConfig) bool {
	if !exactEndpoint(a.Endpoint, b.Endpoint) {
		return false
	}
	a.Connection.DSN = ""
	b.Connection.DSN = ""
	return reflect.DeepEqual(a, b)
}

// CheckBoot never enables an unfinished migration. For terminal/prepared state,
// the caller must supply the endpoint observed through the new connection before
// any schema migration or writer starts. Successful credential rotation updates
// only an existing active profile; historical activation receipts stay intact.
func (c *Control) CheckBoot(actual ActiveConfig) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.state()
	if err != nil {
		return false, err
	}
	if s == nil {
		return true, nil
	}
	var expected ActiveConfig
	switch s.Phase {
	case Prepared, Aborted:
		expected = s.Source
	case Completed:
		expected = s.Target
	default:
		return false, nil
	}
	if !sameBootProfile(actual, expected) {
		return false, nil
	}
	active, err := c.active()
	if err != nil {
		return false, err
	}
	if s.Phase == Completed && active == nil {
		return false, ErrJournal
	}
	if active != nil && !sameBootProfile(*active, expected) {
		return false, ErrIdentity
	}
	// Invalidate the old approval before persisting changed credentials. If the
	// second write fails, startup stops and reopening can reconcile the same
	// authenticated database without ever starting the stale prepared task.
	if s.Phase == Prepared && profileDigest(actual) != profileDigest(s.Source) {
		s.Phase = Aborted
		s.Revision++
		s.Source = actual
		if err = c.write("state", s); err != nil {
			return false, err
		}
	}
	if active != nil && profileDigest(*active) != profileDigest(actual) {
		if err = c.write("active", actual); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ConfirmActivation requires a new process, exact persisted configuration and a
// fresh read-only target verification against the saved report. The controller
// must additionally finish key/file/session checks before invoking this method.
// The completed record is persisted BEFORE any business writer is started.
func (c *Control) ConfirmActivation(task string, revision uint64, actual ActiveConfig, verified *CopyReport) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transition(task, revision, AwaitingRestart, Completed, func(s *State) error {
		if s.Owner == c.owner {
			return ErrLocked
		}
		active, err := c.active()
		if err != nil || active == nil {
			return ErrJournal
		}
		if !exactEndpoint(actual.Endpoint, s.Target.Endpoint) || profileDigest(actual) != profileDigest(*active) || profileDigest(actual) != profileDigest(s.Target) || !validReport(verified) || reportDigest(verified) != reportDigest(s.Report) {
			return ErrMismatch
		}
		s.Owner = c.owner
		return nil
	})
}

// AbortBeforeActivation retains the untouched source and all target evidence.
// Once activation intent exists, rollback needs its own verified restart flow;
// after Completed a reverse migration is required, never this method.
func (c *Control) AbortBeforeActivation(task string, revision uint64) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.state()
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrInvalidState
	}
	switch s.Phase {
	case Prepared, Quiescing, Frozen, Copying, Verified:
	default:
		return nil, ErrInvalidState
	}
	return c.transition(task, revision, s.Phase, Aborted, nil)
}

// stageSourceRestore durably selects the original source before its admission
// fence can be cleared. The unfinished journal keeps normal startup closed. If
// persistence is unknown, the caller returns without changing the source DB.
func (c *Control) stageSourceRestore(task string, revision uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, e := c.state()
	if e != nil {
		return e
	}
	if s == nil || s.TaskID != task || s.Revision != revision || s.Phase == Completed || s.Phase == Aborted {
		return ErrInvalidState
	}
	active, e := c.active()
	if e != nil {
		return e
	}
	if active != nil {
		if profileDigest(*active) == profileDigest(s.Source) {
			return nil
		}
		if profileDigest(*active) != profileDigest(s.Target) {
			return ErrIdentity
		}
	}
	return c.write("active", s.Source)
}

// RestoreSource records completion only after the controller clears its own
// source fence and verifies the durable original profile. Any interruption before
// this record leaves recovery available; Completed always requires reverse copy.
func (c *Control) RestoreSource(task string, revision uint64) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, e := c.state()
	if e != nil {
		return nil, e
	}
	if s == nil || s.TaskID != task || s.Revision != revision || s.Phase == Completed || s.Phase == Aborted {
		return nil, ErrInvalidState
	}
	active, e := c.active()
	if e != nil {
		return nil, e
	}
	if active == nil || profileDigest(*active) != profileDigest(s.Source) {
		return nil, ErrIdentity
	}
	return c.transition(task, revision, s.Phase, Aborted, nil)
}

// Finished journals are historical identity/configuration evidence. They must
// survive later application schema upgrades. A prepared task can be cancelled or
// replaced by a fresh preflight, but cannot start against a changed manifest.
func validStateManifest(s State) bool {
	raw, e := hex.DecodeString(s.Manifest)
	if e != nil || len(raw) != 32 {
		return false
	}
	switch s.Phase {
	case Completed, Aborted, Prepared:
		return true
	}
	return s.Manifest == ManifestDigest()
}

func (c *Control) RecordProgress(task string, revision uint64, proof TableProof) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transition(task, revision, Copying, Copying, func(s *State) error {
		tables := Manifest()
		position := -1
		for i, t := range tables {
			if t.Name == proof.Table {
				position = i
				break
			}
		}
		if position < 0 || position > len(s.Progress) || proof.Rows < 0 {
			return ErrMismatch
		}
		digest, e := hex.DecodeString(proof.SHA256)
		if e != nil || len(digest) != 32 {
			return ErrMismatch
		}
		s.Progress = append(s.Progress[:position], proof)
		return nil
	})
}

// ReconcileActivation is an explicit recovery action after the intent was saved.
// It inspects the authenticated active configuration before completing the local
// handoff. An unrelated profile fails closed; no database writes are retried.
func (c *Control) ReconcileActivation(task string, revision uint64) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.state()
	if err != nil {
		return nil, err
	}
	if s == nil || s.TaskID != task || s.Revision != revision || s.Phase != ActivationIntent {
		return nil, ErrInvalidState
	}
	active, err := c.active()
	if err != nil {
		return nil, err
	}
	if active != nil && profileDigest(*active) != profileDigest(s.Target) && profileDigest(*active) != profileDigest(s.Source) {
		return nil, ErrIdentity
	}
	if active == nil || profileDigest(*active) != profileDigest(s.Target) {
		if err = c.write("active", s.Target); err != nil {
			return nil, err
		}
	}
	return c.transition(task, revision, ActivationIntent, AwaitingRestart, nil)
}
