package authlogin

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"time"
)

func (s *Service) tenantMode(ctx context.Context, q querier) (bool, error) {
	dbtx, ok := q.(configstate.DBTX)
	if !ok {
		return false, ErrFlowInvalid
	}
	return tenancy.Enabled(configstate.WithTxContext(ctx, s.db, dbtx), s.db)
}
func (s *Service) lockTenant(ctx context.Context, tx *sql.Tx, token string, recent bool) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE selfupdate_control SET task_id=task_id WHERE id=1`); err != nil {
		return 0, err
	}
	if recent {
		if err := configstate.CheckMaintenanceAdmission(ctx, tx); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE access_control SET mode_epoch=mode_epoch WHERE id=1`); err != nil {
		return 0, err
	}
	if token == "" {
		return 0, nil
	}
	account, err := tenancy.VerifyAccountSession(ctx, tx, token, false)
	if err != nil {
		return 0, err
	}
	if recent && !tenancy.RecentAccountPassword(ctx, tx, account) {
		return 0, ErrReauthRequired
	}
	return int64(account.User.SecurityGeneration), nil
}
func (s *Service) personalRow(ctx context.Context, q querier, p, user string) (row, error) {
	r, err := s.read(ctx, q, p)
	if err != nil {
		return r, err
	}
	r.Binding = nil
	r.identityGeneration = 0
	if user == "" {
		return r, nil
	}
	var label, issuer, subject string
	var created int64
	err = q.QueryRowContext(ctx, `SELECT issuer,subject,display_label,generation,created_at FROM access_identities WHERE user_id=? AND provider=?`, user, p).Scan(&issuer, &subject, &label, &r.identityGeneration, &created)
	if err == sql.ErrNoRows {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.Binding = &Binding{label, issuer, subject, time.Unix(created, 0).UTC().Format(time.RFC3339)}
	return r, nil
}
func (s *Service) checkTenantFlow(ctx context.Context, q querier, f flow, token string) (row, error) {
	enabled, err := s.tenantMode(ctx, q)
	if err != nil || !enabled {
		return row{}, ErrFlowInvalid
	}
	var epoch uint64
	if q.QueryRowContext(ctx, `SELECT mode_epoch FROM access_control WHERE id=1`).Scan(&epoch) != nil || epoch != f.modeEpoch {
		return row{}, ErrFlowInvalid
	}
	expiry, err := time.Parse(time.RFC3339, f.expires)
	if err != nil || !s.now().Before(expiry) {
		return row{}, ErrFlowExpired
	}
	r, err := s.personalRow(ctx, q, f.provider, f.userID.String)
	if err != nil {
		return r, err
	}
	if r.ConfigVersion != f.config || r.generation != f.generation || !r.Enabled {
		return r, ErrFlowInvalid
	}
	if f.purpose == "bind" {
		if !f.userID.Valid || f.userID.String == "" || token == "" || subtle.ConstantTimeCompare([]byte(digest(token)), []byte(f.sessionHash)) != 1 {
			return r, ErrFlowInvalid
		}
		dbtx := q.(configstate.DBTX)
		account, err := tenancy.VerifyAccountSession(ctx, dbtx, token, false)
		if err != nil || account.User.ID != f.userID.String || int64(account.User.SecurityGeneration) != f.securityGeneration || r.identityGeneration != f.identityGeneration {
			return r, ErrFlowInvalid
		}
	} else if f.userID.Valid || f.securityGeneration != 0 {
		return r, ErrFlowInvalid
	}
	return r, nil
}
func (s *Service) personalSettings(ctx context.Context, token string) (Snapshot, error) {
	account, err := tenancy.VerifyAccountSession(ctx, s.db, token, false)
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{Providers: make([]ProviderSettings, 0, 5), CanManage: account.User.InstanceAdmin}
	if account.User.InstanceAdmin {
		if err = s.db.QueryRowContext(ctx, `SELECT visibility_revision FROM login_settings WHERE id=1`).Scan(&out.VisibilityVersion); err != nil {
			return out, err
		}
	}
	for _, provider := range Providers {
		r, err := s.personalRow(ctx, s.db, provider, account.User.ID)
		if err != nil {
			return out, err
		}
		if !account.User.InstanceAdmin {
			r.ClientID = ""
			r.MicrosoftTenant = ""
			r.GiteeMode = ""
			r.HasSecret = false
			r.CallbackURL = ""
		}
		out.Providers = append(out.Providers, r.ProviderSettings)
	}
	var verified sql.NullInt64
	if s.db.QueryRowContext(ctx, `SELECT password_verified_at FROM access_sessions WHERE token_hash=?`, account.Actor.SessionHash).Scan(&verified) == nil && verified.Valid {
		until := time.Unix(verified.Int64, 0).Add(auth.RecentPasswordWindow)
		if s.now().Before(until) {
			out.ReauthenticatedUntil = until.UTC().Format(time.RFC3339)
		}
	}
	return out, nil
}
func (s *Service) personalMutation(ctx context.Context, token, p string, version int64, change func(*sql.Tx, tenancy.Session, row) error) (Mutation, error) {
	if !ValidProvider(p) {
		return Mutation{}, ErrBadRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, err
	}
	defer tx.Rollback()
	if _, err = s.lockTenant(ctx, tx, token, true); err != nil {
		return Mutation{}, err
	}
	account, err := tenancy.VerifyAccountSession(ctx, tx, token, false)
	if err != nil {
		return Mutation{}, err
	}
	r, err := s.personalRow(ctx, tx, p, account.User.ID)
	if err != nil {
		return Mutation{}, err
	}
	if r.ConfigVersion != version {
		return Mutation{}, ErrConflict
	}
	if err = change(tx, account, r); err != nil {
		return Mutation{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM access_sessions WHERE user_id=? AND auth_source=?`, account.User.ID, p); err != nil {
		return Mutation{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE login_flows SET status='invalidated',candidate_cipher=NULL,verifier_cipher=NULL WHERE access_user_id=? AND provider=? AND status IN ('created','processing','candidate')`, account.User.ID, p); err != nil {
		return Mutation{}, err
	}
	after, err := s.personalRow(ctx, tx, p, account.User.ID)
	if err != nil {
		return Mutation{}, err
	}
	if !account.User.InstanceAdmin {
		after.ClientID = ""
		after.MicrosoftTenant = ""
		after.GiteeMode = ""
		after.HasSecret = false
		after.CallbackURL = ""
	}
	if err = tx.Commit(); err != nil {
		return Mutation{}, err
	}
	return Mutation{after.ProviderSettings, account.AuthSource == p}, nil
}
func (s *Service) confirmPersonal(ctx context.Context, token, p, id string, version int64) (Mutation, error) {
	return s.personalMutation(ctx, token, p, version, func(tx *sql.Tx, account tenancy.Session, r row) error {
		f, err := readFlow(ctx, tx, id)
		if err != nil {
			return err
		}
		if f.modeEpoch == 0 || f.provider != p || f.purpose != "bind" || f.status != "candidate" || !f.candidateExpires.Valid {
			return ErrCandidateInvalid
		}
		expiry, err := time.Parse(time.RFC3339, f.candidateExpires.String)
		if err != nil || !s.now().Before(expiry) {
			return ErrFlowExpired
		}
		if _, err = s.checkTenantFlow(ctx, tx, f, token); err != nil {
			return err
		}
		var envelope identityEnvelope
		if err = s.open(f.candidate, &envelope); err != nil || envelope.Purpose != "platform-login-candidate" || envelope.Flow != id || envelope.Provider != p {
			return ErrCandidateInvalid
		}
		identity := envelope.Identity
		if len(identity.Issuer) > 512 || len(identity.Subject) > 256 {
			return ErrIdentityInvalid
		}
		keyBytes, _ := json.Marshal([]string{p, identity.Issuer, identity.Subject})
		key := digest(string(keyBytes))
		var other int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_identities WHERE identity_key=? AND user_id<>?`, key, account.User.ID).Scan(&other); err != nil {
			return err
		}
		if other != 0 {
			return ErrIdentityDenied
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM access_identities WHERE user_id=? AND provider=?`, account.User.ID, p); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO access_identities(id,user_id,provider,issuer,subject,identity_key,display_label,generation,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), account.User.ID, p, identity.Issuer, identity.Subject, key, identity.DisplayLabel, r.identityGeneration+1, s.now().Unix()); err != nil {
			return ErrIdentityDenied
		}
		_, err = tx.ExecContext(ctx, `UPDATE login_flows SET status='completed',candidate_cipher=NULL,verifier_cipher=NULL WHERE flow_id=?`, id)
		return err
	})
}
func (s *Service) unbindPersonal(ctx context.Context, token, p string, version int64) (Mutation, error) {
	return s.personalMutation(ctx, token, p, version, func(tx *sql.Tx, account tenancy.Session, r row) error {
		// Password-less accounts need a separate verified recovery path before they
		// can remove their final login identity; matching an email is never enough.
		var password sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT password_hash FROM access_users WHERE id=?`, account.User.ID).Scan(&password); err != nil {
			return err
		}
		if !password.Valid || password.String == "" {
			var others int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_identities WHERE user_id=? AND provider<>?`, account.User.ID, p).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return ErrIdentityDenied
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM access_identities WHERE user_id=? AND provider=?`, account.User.ID, p)
		return err
	})
}
