package authlogin

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/auth"
	"strings"
	"time"
)

type StartResult struct {
	FlowID       string `json:"flowId"`
	AuthorizeURL string `json:"authorizeURL"`
	ExpiresAt    string `json:"expiresAt"`
	BrowserNonce string `json:"-"`
}
type Candidate struct {
	FlowID        string `json:"flowId"`
	Provider      string `json:"provider"`
	DisplayLabel  string `json:"displayLabel"`
	ExpiresAt     string `json:"expiresAt"`
	ConfigVersion int64  `json:"configVersion"`
}
type Completion struct {
	Session               *auth.Session
	ReturnTo, BindingFlow string
}
type flow struct {
	id, stateHash, browserHash, purpose, provider              string
	config, generation, identityGeneration, securityGeneration int64
	sessionHash                                                string
	verifier                                                   []byte
	nonceHash, returnTo, status, expires                       string
	candidate                                                  []byte
	candidateExpires                                           sql.NullString
}
type verifierEnvelope struct{ Purpose, Flow, Provider, Verifier string }
type identityEnvelope struct {
	Purpose, Flow, Provider string
	Identity                Identity
}

const flowQuery = `SELECT flow_id,state_hash,browser_hash,purpose,provider,config_version,auth_generation,identity_generation,security_generation,session_hash,verifier_cipher,nonce_hash,return_to,status,expires_at,candidate_cipher,candidate_expires_at FROM login_flows WHERE flow_id=?`

func readFlow(ctx context.Context, q querier, id string) (flow, error) {
	var f flow
	e := q.QueryRowContext(ctx, flowQuery, id).Scan(&f.id, &f.stateHash, &f.browserHash, &f.purpose, &f.provider, &f.config, &f.generation, &f.identityGeneration, &f.securityGeneration, &f.sessionHash, &f.verifier, &f.nonceHash, &f.returnTo, &f.status, &f.expires, &f.candidate, &f.candidateExpires)
	if e == sql.ErrNoRows {
		return f, ErrFlowInvalid
	}
	return f, e
}

// FlowIDFromState validates only a random lookup handle; full state is always hashed and checked.
func FlowIDFromState(state string) string {
	parts := strings.Split(state, ".")
	if len(parts) != 2 || len(parts[0]) != 32 || len(parts[1]) != 64 {
		return ""
	}
	for _, c := range parts[0] + parts[1] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return ""
		}
	}
	return parts[0]
}
func (s *Service) Start(ctx context.Context, p, purpose, token, returnTo string, expectedVersion int64) (StartResult, error) {
	if !ValidProvider(p) || (purpose != "login" && purpose != "bind") {
		return StartResult{}, ErrBadRequest
	}
	safe, e := SafeReturnTo(returnTo)
	if e != nil {
		return StartResult{}, e
	}
	adapter := s.adapters[p]
	if adapter == nil {
		return StartResult{}, ErrNotConfigured
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return StartResult{}, e
	}
	defer tx.Rollback()
	generation, e := s.lock(ctx, tx, token, purpose == "bind")
	if e != nil {
		return StartResult{}, e
	}
	r, e := s.read(ctx, tx, p)
	if e != nil {
		return StartResult{}, e
	}
	if purpose == "login" && (!r.Enabled || r.Binding == nil) {
		if !r.Configured {
			return StartResult{}, ErrNotConfigured
		}
		return StartResult{}, ErrDisabled
	}
	if purpose == "bind" && (token == "" || expectedVersion != r.ConfigVersion) {
		return StartResult{}, ErrConflict
	}
	app, e := s.app(r)
	if e != nil {
		return StartResult{}, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_flows WHERE expires_at>? AND status IN ('created','processing','candidate')`, s.now().UTC().Format(time.RFC3339)).Scan(&count); e != nil {
		return StartResult{}, e
	}
	if count >= 256 {
		return StartResult{}, ErrRateLimited
	}
	id, e := randomHex(16)
	if e != nil {
		return StartResult{}, e
	}
	stateRandom, e := randomHex(32)
	if e != nil {
		return StartResult{}, e
	}
	state := id + "." + stateRandom
	browser, e := randomHex(32)
	if e != nil {
		return StartResult{}, e
	}
	verifier, e := randomHex(32)
	if e != nil {
		return StartResult{}, e
	}
	nonce, e := randomHex(32)
	if e != nil {
		return StartResult{}, e
	}
	cipher, e := s.seal(verifierEnvelope{"platform-login-verifier", id, p, verifier})
	if e != nil {
		return StartResult{}, ErrNotConfigured
	}
	now := s.now().UTC()
	expiry := now.Add(10 * time.Minute)
	sessionHash := ""
	if token != "" {
		sessionHash = digest(token)
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO login_flows(flow_id,state_hash,browser_hash,purpose,provider,config_version,auth_generation,identity_generation,security_generation,session_hash,verifier_cipher,nonce_hash,return_to,status,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'created',?,?)`, id, digest(state), digest(browser), purpose, p, r.ConfigVersion, r.generation, r.identityGeneration, generation, sessionHash, cipher, digest(nonce), safe, expiry.Format(time.RFC3339), now.Format(time.RFC3339))
	if e != nil {
		return StartResult{}, e
	}
	if e = tx.Commit(); e != nil {
		return StartResult{}, e
	}
	authorize, e := adapter.Authorize(ctx, app, AuthorizeParams{State: state, Verifier: verifier, Nonce: nonce})
	if e != nil {
		s.fail(ctx, id, "failed")
		return StartResult{}, ErrProviderUnavailable
	}
	return StartResult{id, authorize, expiry.Format(time.RFC3339), browser}, nil
}
func (s *Service) fail(ctx context.Context, id, status string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE login_flows SET status=?,verifier_cipher=NULL,candidate_cipher=NULL WHERE flow_id=? AND status IN ('created','processing')`, status, id)
}
func (s *Service) checkFlow(ctx context.Context, q querier, f flow, token string) (row, error) {
	expiry, e := time.Parse(time.RFC3339, f.expires)
	if e != nil || !s.now().Before(expiry) {
		return row{}, ErrFlowExpired
	}
	var generation int64
	if e = q.QueryRowContext(ctx, `SELECT security_generation FROM admin_user WHERE id=1`).Scan(&generation); e != nil {
		return row{}, e
	}
	r, e := s.read(ctx, q, f.provider)
	if e != nil {
		return r, e
	}
	if generation != f.securityGeneration || r.ConfigVersion != f.config || r.generation != f.generation || r.identityGeneration != f.identityGeneration {
		return r, ErrFlowInvalid
	}
	if f.purpose == "login" && (!r.Enabled || r.Binding == nil) {
		return r, ErrDisabled
	}
	if f.purpose == "bind" {
		if token == "" || subtle.ConstantTimeCompare([]byte(digest(token)), []byte(f.sessionHash)) != 1 {
			return r, ErrFlowInvalid
		}
		var expires string
		if q.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE token=?`, token).Scan(&expires) != nil {
			return r, auth.ErrSessionNotFound
		}
		t, e := time.Parse(time.RFC3339, expires)
		if e != nil || !s.now().Before(t) {
			return r, auth.ErrSessionNotFound
		}
	}
	return r, nil
}

// Complete claims a flow before a single network exchange. No DB transaction spans that exchange.
func (s *Service) Complete(ctx context.Context, p, state, browser, token, code string, cancelled bool) (Completion, error) {
	id := FlowIDFromState(state)
	if id == "" || !ValidProvider(p) || len(browser) != 64 {
		return Completion{}, ErrFlowInvalid
	}
	f, e := readFlow(ctx, s.db, id)
	if e != nil {
		return Completion{}, e
	}
	if f.provider != p || subtle.ConstantTimeCompare([]byte(f.stateHash), []byte(digest(state))) != 1 || subtle.ConstantTimeCompare([]byte(f.browserHash), []byte(digest(browser))) != 1 {
		return Completion{}, ErrFlowInvalid
	}
	if f.purpose == "login" {
		token = ""
	}
	if f.status != "created" {
		return Completion{}, ErrFlowConsumed
	}
	r, e := s.checkFlow(ctx, s.db, f, token)
	if e != nil {
		return Completion{}, e
	}
	result, e := s.db.ExecContext(ctx, `UPDATE login_flows SET status='processing' WHERE flow_id=? AND status='created'`, id)
	if e != nil {
		return Completion{}, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Completion{}, ErrFlowConsumed
	}
	succeeded := false
	defer func() {
		if !succeeded {
			s.fail(context.WithoutCancel(ctx), id, "failed")
		}
	}()
	if cancelled {
		s.fail(ctx, id, "cancelled")
		return Completion{}, ErrProviderRejected
	}
	if code == "" || len(code) > 8192 {
		s.fail(ctx, id, "failed")
		return Completion{}, ErrFlowInvalid
	}
	var envelope verifierEnvelope
	if e = s.open(f.verifier, &envelope); e != nil || envelope.Purpose != "platform-login-verifier" || envelope.Flow != id || envelope.Provider != p {
		s.fail(ctx, id, "failed")
		return Completion{}, ErrFlowInvalid
	}
	app, e := s.app(r)
	if e != nil {
		s.fail(ctx, id, "failed")
		return Completion{}, e
	}
	adapter := s.adapters[p]
	if adapter == nil {
		s.fail(ctx, id, "failed")
		return Completion{}, ErrProviderUnavailable
	}
	identity, e := adapter.Exchange(ctx, app, ExchangeParams{Code: code, Verifier: envelope.Verifier, NonceHash: f.nonceHash})
	if e != nil {
		s.fail(ctx, id, "failed")
		return Completion{}, e
	}
	if identity.Provider != p || !validText(identity.Issuer, 2048) || !validText(identity.Subject, 2048) || len(identity.DisplayLabel) > 512 {
		s.fail(ctx, id, "failed")
		return Completion{}, ErrIdentityInvalid
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		s.fail(ctx, id, "failed")
		return Completion{}, e
	}
	defer tx.Rollback()
	if _, e = s.lock(ctx, tx, token, false); e != nil {
		return Completion{}, e
	}
	current, e := readFlow(ctx, tx, id)
	if e != nil {
		return Completion{}, e
	}
	if current.status != "processing" {
		return Completion{}, ErrFlowConsumed
	}
	r, e = s.checkFlow(ctx, tx, f, token)
	if e != nil {
		return Completion{}, e
	}
	out := Completion{ReturnTo: f.returnTo}
	if f.purpose == "bind" {
		cipher, e := s.seal(identityEnvelope{"platform-login-candidate", id, p, identity})
		if e != nil {
			return Completion{}, e
		}
		expiry := s.now().UTC().Add(5 * time.Minute)
		_, e = tx.ExecContext(ctx, `UPDATE login_flows SET status='candidate',verifier_cipher=NULL,candidate_cipher=?,candidate_expires_at=?,expires_at=? WHERE flow_id=?`, cipher, expiry.Format(time.RFC3339), expiry.Format(time.RFC3339), id)
		if e != nil {
			return Completion{}, e
		}
		out.BindingFlow = id
	} else {
		if r.Binding.Issuer != identity.Issuer || r.Binding.Subject != identity.Subject {
			return Completion{}, ErrIdentityDenied
		}
		sess, e := auth.NewSession(s.now().UTC(), p, r.generation, r.identityGeneration)
		if e != nil {
			return Completion{}, e
		}
		if e = auth.InsertSession(tx, sess); e != nil {
			return Completion{}, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE login_flows SET status='completed',verifier_cipher=NULL,candidate_cipher=NULL WHERE flow_id=?`, id); e != nil {
			return Completion{}, e
		}
		out.Session = sess
	}
	if e = tx.Commit(); e != nil {
		return Completion{}, e
	}
	succeeded = true
	return out, nil
}
func (s *Service) Pending(ctx context.Context, token, id string) (Candidate, error) {
	f, e := readFlow(ctx, s.db, id)
	if e != nil {
		return Candidate{}, e
	}
	if f.status != "candidate" || !f.candidateExpires.Valid {
		return Candidate{}, ErrCandidateInvalid
	}
	expiry, e := time.Parse(time.RFC3339, f.candidateExpires.String)
	if e != nil || !s.now().Before(expiry) {
		return Candidate{}, ErrFlowExpired
	}
	if _, e = s.checkFlow(ctx, s.db, f, token); e != nil {
		return Candidate{}, e
	}
	var envelope identityEnvelope
	if e = s.open(f.candidate, &envelope); e != nil || envelope.Purpose != "platform-login-candidate" || envelope.Flow != id || envelope.Provider != f.provider {
		return Candidate{}, ErrCandidateInvalid
	}
	return Candidate{id, f.provider, envelope.Identity.DisplayLabel, f.candidateExpires.String, f.config}, nil
}
func (s *Service) Confirm(ctx context.Context, token, p, id string, version int64) (Mutation, error) {
	// mutation serializes confirmation with configuration/credential and generation changes.
	return s.mutation(ctx, token, p, version, func(tx *sql.Tx, r row) error {
		f, e := readFlow(ctx, tx, id)
		if e != nil {
			return e
		}
		if f.provider != p || f.purpose != "bind" || f.status != "candidate" || !f.candidateExpires.Valid {
			return ErrCandidateInvalid
		}
		expiry, e := time.Parse(time.RFC3339, f.candidateExpires.String)
		if e != nil || !s.now().Before(expiry) {
			return ErrFlowExpired
		}
		if _, e = s.checkFlow(ctx, tx, f, token); e != nil {
			return e
		}
		var envelope identityEnvelope
		if e = s.open(f.candidate, &envelope); e != nil || envelope.Purpose != "platform-login-candidate" || envelope.Flow != id || envelope.Provider != p {
			return ErrCandidateInvalid
		}
		identity := envelope.Identity
		if _, e = tx.ExecContext(ctx, `DELETE FROM login_identities WHERE provider=?`, p); e != nil {
			return e
		}
		// auth_generation also advances, so an unbind/rebind cannot resurrect an earlier session.
		if _, e = tx.ExecContext(ctx, `INSERT INTO login_identities(provider,issuer,subject,display_label,identity_generation,bound_at) VALUES(?,?,?,?,?,?)`, p, identity.Issuer, identity.Subject, identity.DisplayLabel, r.identityGeneration+1, s.now().UTC().Format(time.RFC3339)); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE login_provider_settings SET enabled=1 WHERE provider=?`, p); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE login_flows SET status='completed',candidate_cipher=NULL,verifier_cipher=NULL WHERE flow_id=?`, id)
		return e
	})
}
