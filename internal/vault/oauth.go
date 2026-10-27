package vault

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/completedrefs"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

const (
	OAuthActive      = "active"
	OAuthRefreshing  = "refreshing"
	OAuthUnavailable = "refresh_unavailable"
	OAuthReauthorize = "reauthorization_required"
	OAuthUncertain   = "refresh_uncertain"
)

// OAuthInfo is the complete public view. Token material and application secrets
// never have a JSON representation in the credential API.
type OAuthInfo struct {
	Managed     bool       `json:"managed"`
	Provider    string     `json:"provider"`
	Account     string     `json:"account"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	Refreshable bool       `json:"refreshable"`
	Revision    int64      `json:"revision"`
}

type OAuthBinding struct {
	CredentialID                                         string
	Provider, Issuer, ClientID, RedirectURI, AppRevision string
	AccountID, Login                                     string
	ExpiresAt, RefreshExpiresAt, RefreshAfter            *time.Time
	Revision                                             int64
	State, ClaimID, ClaimOwner                           string
	RefreshCiphertext, AccessCiphertext                  []byte
}

type OAuthTokens struct {
	AccessToken, RefreshToken                 string
	ExpiresAt, RefreshExpiresAt, RefreshAfter *time.Time
}

type OAuthTarget struct {
	CredentialID            string
	Revision                int64
	CipherDigest            string
	Adopt                   bool
	ClaimID                 string
	PreviousAuthorizationID string
	PreviousRefreshID       string
}

// OAuthRepository owns the short SQL boundary for both token halves. It never
// performs network I/O or expires claims/references using a wall-clock lease.
type OAuthRepository struct {
	db    *sql.DB
	vault Vault
}

func NewOAuthRepository(db *sql.DB, v Vault) *OAuthRepository {
	return &OAuthRepository{db: db, vault: v}
}

func oauthGitUsername(provider, login string) string {
	if provider == "gitlab" || provider == "custom" {
		return "oauth2"
	}
	return login
}

func OAuthAppRevision(clientID, baseURL string, ciphertext []byte, enabled bool) string {
	b, _ := json.Marshal([]any{clientID, baseURL, hex.EncodeToString(ciphertext), enabled})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func oauthAppMatches(ctx context.Context, q configstate.DBTX, b *OAuthBinding) bool {
	var client, base string
	var secret []byte
	var enabled int
	if err := q.QueryRowContext(ctx, "SELECT client_id,base_url,client_secret_ciphertext,enabled FROM oauth_apps WHERE provider=?", b.Provider).Scan(&client, &base, &secret, &enabled); err != nil {
		return false
	}
	return enabled == 1 && b.AppRevision == OAuthAppRevision(client, base, secret, true)
}

func nullableMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}
func timeFromNull(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}
func cipherDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func readOAuth(ctx context.Context, q configstate.DBTX, id string) (*OAuthBinding, error) {
	var b OAuthBinding
	var expires, refreshExpires, refreshAfter sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT o.credential_id,o.provider,o.issuer,o.client_id,o.redirect_uri,o.app_revision,o.account_id,o.login,o.refresh_ciphertext,o.expires_at,o.refresh_expires_at,o.refresh_after,o.revision,o.state,o.claim_id,o.claim_owner,c.ciphertext FROM oauth_credentials o JOIN credentials c ON c.id=o.credential_id WHERE o.credential_id=?`, id).Scan(&b.CredentialID, &b.Provider, &b.Issuer, &b.ClientID, &b.RedirectURI, &b.AppRevision, &b.AccountID, &b.Login, &b.RefreshCiphertext, &expires, &refreshExpires, &refreshAfter, &b.Revision, &b.State, &b.ClaimID, &b.ClaimOwner, &b.AccessCiphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.ExpiresAt = timeFromNull(expires)
	b.RefreshExpiresAt = timeFromNull(refreshExpires)
	b.RefreshAfter = timeFromNull(refreshAfter)
	return &b, nil
}
func (r *OAuthRepository) Binding(ctx context.Context, id string) (*OAuthBinding, error) {
	return readOAuth(ctx, configstate.Queryer(ctx, r.db), id)
}

func oauthUses(ctx context.Context, q configstate.DBTX, id string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=?", id).Scan(&n)
	return n, err
}

func (r *OAuthRepository) Target(ctx context.Context, id string, adopt bool) (OAuthTarget, error) {
	var out OAuthTarget
	err := configstate.WithWriteTx(ctx, r.db, func(q configstate.DBTX) error {
		if err := configstate.LockEpochs(ctx, q, r.db, configstate.EpochSecrets); err != nil {
			return err
		}
		var typ string
		var cipher []byte
		if err := q.QueryRowContext(ctx, "SELECT type,ciphertext FROM credentials WHERE id=?", id).Scan(&typ, &cipher); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if typ != TypeGitToken && typ != TypeGitHTTP {
			return ErrInvalidType
		}
		b, err := readOAuth(ctx, q, id)
		if err != nil {
			return err
		}
		if b == nil && !adopt {
			return ErrOAuthIdentity
		}
		n, err := oauthUses(ctx, q, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrOAuthBusy
		}
		out = OAuthTarget{CredentialID: id, CipherDigest: cipherDigest(cipher), Adopt: adopt}
		if b != nil {
			out.Revision = b.Revision
			if b.State == OAuthRefreshing {
				out.PreviousRefreshID = b.ClaimID
			}
		}
		var existingClaim string
		if e := q.QueryRowContext(ctx, "SELECT claim_id FROM oauth_authorizations WHERE credential_id=?", id).Scan(&existingClaim); e == nil {
			out.PreviousAuthorizationID = existingClaim
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		return nil
	})
	return out, err
}

// ClaimAuthorization fences Git users before exchanging a browser code, not
// after the provider may have invalidated an older access token.
func (r *OAuthRepository) ClaimAuthorization(ctx context.Context, target OAuthTarget, owner string) (OAuthTarget, error) {
	err := configstate.WithWriteTx(ctx, r.db, func(q configstate.DBTX) error {
		if err := configstate.LockEpochs(ctx, q, r.db, configstate.EpochSecrets); err != nil {
			return err
		}
		var cipher []byte
		if err := q.QueryRowContext(ctx, "SELECT ciphertext FROM credentials WHERE id=?", target.CredentialID).Scan(&cipher); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if cipherDigest(cipher) != target.CipherDigest {
			return ErrConflict
		}
		b, err := readOAuth(ctx, q, target.CredentialID)
		if err != nil {
			return err
		}
		if b != nil && b.Revision != target.Revision {
			return ErrConflict
		}
		if b != nil && b.State == OAuthRefreshing {
			if b.ClaimID != target.PreviousRefreshID || target.PreviousRefreshID == "" {
				return ErrOAuthBusy
			}
			if _, err = q.ExecContext(ctx, "UPDATE oauth_credentials SET state=? WHERE credential_id=? AND revision=? AND claim_id=?", OAuthUncertain, target.CredentialID, target.Revision, target.PreviousRefreshID); err != nil {
				return err
			}
		}
		n, err := oauthUses(ctx, q, target.CredentialID)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrOAuthBusy
		}
		var previous string
		err = q.QueryRowContext(ctx, "SELECT claim_id FROM oauth_authorizations WHERE credential_id=?", target.CredentialID).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && previous != target.PreviousAuthorizationID {
			return ErrConflict
		}
		if errors.Is(err, sql.ErrNoRows) && target.PreviousAuthorizationID != "" {
			return ErrConflict
		}
		if _, err = q.ExecContext(ctx, "DELETE FROM oauth_authorizations WHERE credential_id=?", target.CredentialID); err != nil {
			return err
		}
		target.ClaimID = uuid.NewString()
		_, err = q.ExecContext(ctx, "INSERT INTO oauth_authorizations(credential_id,claim_id,owner,revision,cipher_digest,state,updated_at) VALUES(?,?,?,?,?,?,?)", target.CredentialID, target.ClaimID, owner, target.Revision, target.CipherDigest, "pending", time.Now().UnixMilli())
		return err
	})
	return target, err
}

func (r *OAuthRepository) ClearAuthorization(ctx context.Context, target OAuthTarget) error {
	if target.ClaimID == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, "DELETE FROM oauth_authorizations WHERE credential_id=? AND claim_id=?", target.CredentialID, target.ClaimID)
	return err
}

// Authorize inserts a new credential or replaces one explicitly bound to the
// one-time browser state. Account IDs are provider identities, never names.
func (r *OAuthRepository) Authorize(ctx context.Context, b OAuthBinding, t OAuthTokens, target OAuthTarget) (string, error) {
	if b.AccountID == "" || b.Login == "" || t.AccessToken == "" {
		return "", ErrOAuthIdentity
	}
	access, err := r.vault.SealSecret([]byte(t.AccessToken))
	if err != nil {
		return "", err
	}
	refresh := []byte{}
	if t.RefreshToken != "" {
		refresh, err = r.vault.SealSecret([]byte(t.RefreshToken))
		if err != nil {
			return "", err
		}
	}
	id := target.CredentialID
	if id == "" {
		id = uuid.NewString()
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	err = configstate.WithWriteTx(ctx, r.db, func(q configstate.DBTX) error {
		if e := configstate.LockEpochs(ctx, q, r.db, configstate.EpochSecrets); e != nil {
			return e
		}
		if !oauthAppMatches(ctx, q, &b) {
			return ErrOAuthIdentity
		}
		revision := int64(1)
		if target.CredentialID == "" {
			_, e := q.ExecContext(ctx, `INSERT INTO credentials(id,name,type,scope,username,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, b.Provider+":"+b.Login, TypeGitToken, "global", oauthGitUsername(b.Provider, b.Login), access, mask(TypeGitToken, t.AccessToken), stamp, stamp)
			if e != nil {
				return e
			}
		} else {
			var claim string
			if e := q.QueryRowContext(ctx, "SELECT claim_id FROM oauth_authorizations WHERE credential_id=?", id).Scan(&claim); e != nil || claim != target.ClaimID {
				return ErrConflict
			}
			var typ string
			var oldCipher []byte
			e := q.QueryRowContext(ctx, "SELECT type,ciphertext FROM credentials WHERE id=?", id).Scan(&typ, &oldCipher)
			if errors.Is(e, sql.ErrNoRows) {
				return ErrNotFound
			}
			if e != nil {
				return e
			}
			if typ != TypeGitToken && typ != TypeGitHTTP {
				return ErrInvalidType
			}
			if cipherDigest(oldCipher) != target.CipherDigest {
				return ErrConflict
			}
			old, e := readOAuth(ctx, q, id)
			if e != nil {
				return e
			}
			if old == nil {
				if !target.Adopt || target.Revision != 0 {
					return ErrOAuthIdentity
				}
			} else {
				if old.Revision != target.Revision || old.Provider != b.Provider || old.Issuer != b.Issuer || old.AccountID != b.AccountID {
					return ErrOAuthIdentity
				}
				revision = old.Revision + 1
			}
			n, e := oauthUses(ctx, q, id)
			if e != nil {
				return e
			}
			if n > 0 {
				return ErrOAuthBusy
			}
			res, e := q.ExecContext(ctx, "UPDATE credentials SET ciphertext=?,masked_value=?,username=?,updated_at=? WHERE id=? AND ciphertext=?", access, mask(typ, t.AccessToken), oauthGitUsername(b.Provider, b.Login), stamp, id, oldCipher)
			if e != nil {
				return e
			}
			n64, e := res.RowsAffected()
			if e != nil {
				return e
			}
			if n64 != 1 {
				return ErrConflict
			}
			if _, e = q.ExecContext(ctx, "DELETE FROM oauth_credentials WHERE credential_id=?", id); e != nil {
				return e
			}
			if _, e = q.ExecContext(ctx, "DELETE FROM oauth_authorizations WHERE credential_id=? AND claim_id=?", id, target.ClaimID); e != nil {
				return e
			}
		}
		_, e := q.ExecContext(ctx, `INSERT INTO oauth_credentials(credential_id,provider,issuer,client_id,redirect_uri,app_revision,account_id,login,refresh_ciphertext,expires_at,refresh_expires_at,refresh_after,revision,state,claim_id,claim_owner,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, b.Provider, b.Issuer, b.ClientID, b.RedirectURI, b.AppRevision, b.AccountID, b.Login, refresh, nullableMillis(t.ExpiresAt), nullableMillis(t.RefreshExpiresAt), nullableMillis(t.RefreshAfter), revision, OAuthActive, "", "", now.UnixMilli())
		if e != nil {
			return e
		}
		return configstate.BumpEpoch(ctx, q, configstate.EpochSecrets)
	})
	return id, err
}

type OAuthAcquisition struct {
	Auth    GitAuth
	UseID   string
	Binding *OAuthBinding
	Action  string
}

// TryAcquire serializes admissions with refresh claims in both SQL dialects.
// References are deleted only when the real operation has returned.
func (r *OAuthRepository) TryAcquire(ctx context.Context, id, owner string, now time.Time) (OAuthAcquisition, error) {
	var out OAuthAcquisition
	err := configstate.WithWriteTx(ctx, r.db, func(q configstate.DBTX) error {
		if e := configstate.LockEpochs(ctx, q, r.db, configstate.EpochSecrets); e != nil {
			return e
		}
		b, e := readOAuth(ctx, q, id)
		if e != nil {
			return e
		}
		out.Binding = b
		var authorizationID, authorizationOwner, authorizationState string
		authErr := q.QueryRowContext(ctx, "SELECT claim_id,owner,state FROM oauth_authorizations WHERE credential_id=?", id).Scan(&authorizationID, &authorizationOwner, &authorizationState)
		if authErr == nil {
			if authorizationState != "pending" {
				return ErrOAuthUncertain
			}
			out.Action = "wait_authorization"
			out.Binding = &OAuthBinding{ClaimID: authorizationID, ClaimOwner: authorizationOwner}
			return nil
		} else if !errors.Is(authErr, sql.ErrNoRows) {
			return authErr
		}
		var typ, username string
		var cipher []byte
		if e = q.QueryRowContext(ctx, "SELECT type,username,ciphertext FROM credentials WHERE id=?", id).Scan(&typ, &username, &cipher); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrNotFound
			}
			return e
		}
		if b != nil {
			if !oauthAppMatches(ctx, q, b) {
				return ErrOAuthIdentity
			}
			switch b.State {
			case OAuthUncertain:
				return ErrOAuthUncertain
			case OAuthReauthorize:
				return ErrOAuthReauthorize
			case OAuthUnavailable:
				return ErrOAuthReauthorize
			case OAuthRefreshing:
				out.Action = "wait_refresh"
				return nil
			}
			// A 30s lead time is bounded to half the declared lifetime at exchange time.
			if b.ExpiresAt != nil && ((b.RefreshAfter != nil && !b.RefreshAfter.After(now)) || !b.ExpiresAt.After(now)) && len(b.RefreshCiphertext) > 0 {
				if b.RefreshExpiresAt != nil && !b.RefreshExpiresAt.After(now) {
					return ErrOAuthReauthorize
				}
				n, e := oauthUses(ctx, q, id)
				if e != nil {
					return e
				}
				if n > 0 {
					out.Action = "wait_users"
					return nil
				}
				b.ClaimID = uuid.NewString()
				b.ClaimOwner = owner
				result, e := q.ExecContext(ctx, "UPDATE oauth_credentials SET state=?,claim_id=?,claim_owner=?,updated_at=? WHERE credential_id=? AND revision=? AND state=?", OAuthRefreshing, b.ClaimID, owner, now.UnixMilli(), id, b.Revision, OAuthActive)
				if e != nil {
					return e
				}
				n64, e := result.RowsAffected()
				if e != nil {
					return e
				}
				if n64 != 1 {
					return ErrConflict
				}
				b.State = OAuthRefreshing
				out.Action = "refresh"
				return nil
			}
			if b.ExpiresAt != nil && !b.ExpiresAt.After(now) {
				return ErrOAuthReauthorize
			}
		}
		generation := int64(0)
		if b != nil {
			generation = b.Revision
		}
		out.UseID = uuid.NewString()
		if _, e = q.ExecContext(ctx, "INSERT INTO oauth_credential_uses(id,credential_id,generation,owner,created_at) VALUES(?,?,?,?,?)", out.UseID, id, generation, owner, now.UnixMilli()); e != nil {
			return e
		}
		plain, e := r.vault.OpenSecret(cipher)
		if e != nil {
			return e
		}
		out.Auth = GitAuth{Type: typ, Username: username, Token: string(plain)}
		if b != nil {
			out.Auth.Username = oauthGitUsername(b.Provider, b.Login)
		}
		out.Action = "use"
		_, e = q.ExecContext(ctx, "UPDATE credentials SET last_used_at=? WHERE id=?", now.UTC().Format(time.RFC3339Nano), id)
		return e
	})
	return out, err
}

func (r *OAuthRepository) Release(ctx context.Context, id, owner string) error {
	if id == "" {
		return nil
	}
	return completedrefs.Complete(ctx, r.db, "oauth_use", id, owner, func(retry context.Context) error {
		_, e := r.db.ExecContext(retry, "DELETE FROM oauth_credential_uses WHERE id=? AND owner=?", id, owner)
		return e
	})
}

func (r *OAuthRepository) CompleteRefresh(ctx context.Context, b *OAuthBinding, t OAuthTokens) error {
	access, e := r.vault.SealSecret([]byte(t.AccessToken))
	if e != nil {
		return e
	}
	refresh := b.RefreshCiphertext
	if t.RefreshToken != "" {
		refresh, e = r.vault.SealSecret([]byte(t.RefreshToken))
		if e != nil {
			return e
		}
	}
	if t.RefreshExpiresAt == nil && t.RefreshToken == "" {
		t.RefreshExpiresAt = b.RefreshExpiresAt
	}
	return configstate.WithWriteTx(ctx, r.db, func(q configstate.DBTX) error {
		if e := configstate.LockEpochs(ctx, q, r.db, configstate.EpochSecrets); e != nil {
			return e
		}
		current, e := readOAuth(ctx, q, b.CredentialID)
		if e != nil {
			return e
		}
		if current == nil || current.Revision != b.Revision || current.ClaimID != b.ClaimID || current.ClaimOwner != b.ClaimOwner || current.State != OAuthRefreshing || cipherDigest(current.AccessCiphertext) != cipherDigest(b.AccessCiphertext) {
			return ErrConflict
		}
		var authorizationCount int
		if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM oauth_authorizations WHERE credential_id=?", b.CredentialID).Scan(&authorizationCount); err != nil {
			return err
		}
		if authorizationCount != 0 {
			return ErrConflict
		}
		if !oauthAppMatches(ctx, q, current) {
			return ErrOAuthIdentity
		}
		var typ string
		if e = q.QueryRowContext(ctx, "SELECT type FROM credentials WHERE id=?", b.CredentialID).Scan(&typ); e != nil {
			return e
		}
		result, e := q.ExecContext(ctx, "UPDATE credentials SET ciphertext=?,masked_value=?,updated_at=? WHERE id=? AND ciphertext=?", access, mask(typ, t.AccessToken), time.Now().UTC().Format(time.RFC3339Nano), b.CredentialID, b.AccessCiphertext)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		result, e = q.ExecContext(ctx, "UPDATE oauth_credentials SET refresh_ciphertext=?,expires_at=?,refresh_expires_at=?,refresh_after=?,revision=revision+1,state=?,claim_id='',claim_owner='',updated_at=? WHERE credential_id=? AND revision=? AND claim_id=?", refresh, nullableMillis(t.ExpiresAt), nullableMillis(t.RefreshExpiresAt), nullableMillis(t.RefreshAfter), OAuthActive, time.Now().UnixMilli(), b.CredentialID, b.Revision, b.ClaimID)
		if e != nil {
			return e
		}
		n, e = result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		return configstate.BumpEpoch(ctx, q, configstate.EpochSecrets)
	})
}

func (r *OAuthRepository) FinishFailure(ctx context.Context, b *OAuthBinding, state string) error {
	if state != OAuthUncertain && state != OAuthReauthorize && state != OAuthUnavailable {
		return ErrConflict
	}
	_, e := r.db.ExecContext(ctx, "UPDATE oauth_credentials SET state=?,updated_at=? WHERE credential_id=? AND revision=? AND claim_id=? AND claim_owner=? AND state=?", state, time.Now().UnixMilli(), b.CredentialID, b.Revision, b.ClaimID, b.ClaimOwner, OAuthRefreshing)
	return e
}

// AuthorizationFailure never invents a binding for an old credential. For an
// existing binding, only the exact browser-snapshotted generation is marked.
func (r *OAuthRepository) AuthorizationFailure(ctx context.Context, target OAuthTarget, state string) error {
	if target.CredentialID == "" {
		return nil
	}
	if state != OAuthUncertain && state != OAuthReauthorize {
		return ErrConflict
	}
	return configstate.WithWriteTx(ctx, r.db, func(q configstate.DBTX) error {
		if err := configstate.LockEpochs(ctx, q, r.db, configstate.EpochSecrets); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, "UPDATE oauth_authorizations SET state=?,updated_at=? WHERE credential_id=? AND claim_id=?", state, time.Now().UnixMilli(), target.CredentialID, target.ClaimID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return nil
		}
		b, err := readOAuth(ctx, q, target.CredentialID)
		if err != nil || b == nil {
			return err
		}
		if b.Revision != target.Revision || cipherDigest(b.AccessCiphertext) != target.CipherDigest {
			return nil
		}
		_, err = q.ExecContext(ctx, "UPDATE oauth_credentials SET state=?,updated_at=? WHERE credential_id=? AND revision=?", state, time.Now().UnixMilli(), target.CredentialID, target.Revision)
		return err
	})
}

func OAuthView(ctx context.Context, db *sql.DB, id string) (*OAuthInfo, error) {
	b, e := readOAuth(ctx, configstate.Queryer(ctx, db), id)
	if e != nil {
		return nil, e
	}
	var authState string
	claimErr := configstate.Queryer(ctx, db).QueryRowContext(ctx, "SELECT state FROM oauth_authorizations WHERE credential_id=?", id).Scan(&authState)
	if claimErr != nil && !errors.Is(claimErr, sql.ErrNoRows) {
		return nil, claimErr
	}
	if b == nil {
		if claimErr == nil {
			return &OAuthInfo{Status: OAuthUncertain}, nil
		}
		return nil, nil
	}
	state := b.State
	if claimErr == nil {
		state = OAuthUncertain
	}
	if !oauthAppMatches(ctx, configstate.Queryer(ctx, db), b) {
		state = OAuthReauthorize
	} else if state == OAuthActive && b.ExpiresAt != nil && !b.ExpiresAt.After(time.Now()) && len(b.RefreshCiphertext) == 0 {
		state = OAuthReauthorize
	}
	return &OAuthInfo{Managed: true, Provider: b.Provider, Account: b.Login, Status: state, ExpiresAt: b.ExpiresAt, Refreshable: len(b.RefreshCiphertext) > 0, Revision: b.Revision}, nil
}

// checkOAuthMutation is called inside the existing secret epoch transaction.
// Renames/scopes retain the binding; direct token replacement requires consent.
func checkOAuthMutation(ctx context.Context, q configstate.DBTX, id string, replace, detach bool) error {
	b, e := readOAuth(ctx, q, id)
	if e != nil {
		return e
	}
	if b == nil {
		if replace || detach {
			n, err := oauthUses(ctx, q, id)
			if err != nil {
				return err
			}
			if n > 0 {
				return ErrOAuthBusy
			}
			_, err = q.ExecContext(ctx, "DELETE FROM oauth_authorizations WHERE credential_id=?", id)
			return err
		}
		return nil
	}
	n, e := oauthUses(ctx, q, id)
	if e != nil {
		return e
	}
	if replace || detach {
		if n > 0 {
			return ErrOAuthBusy
		}
		if !detach {
			return ErrOAuthDetachRequired
		}
	}
	if detach {
		if _, e = q.ExecContext(ctx, "DELETE FROM oauth_authorizations WHERE credential_id=?", id); e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, "DELETE FROM oauth_credentials WHERE credential_id=?", id)
		return e
	}
	return nil
}

func validateOAuthIdentifier(s string) bool {
	return s != "" && len(s) <= 256 && !strings.ContainsAny(s, "\r\n\x00")
}

// ParseAccountID preserves decimal identity exactly, including values beyond
// IEEE754's integer range. JSON numbers must be unsigned canonical integers.
func ParseAccountID(value any) (string, error) {
	var id string
	switch v := value.(type) {
	case string:
		id = v
	case json.Number:
		id = v.String()
		if id == "" || strings.ContainsAny(id, ".eE+-") {
			return "", ErrOAuthIdentity
		}
		if _, e := strconv.ParseUint(id, 10, 64); e != nil {
			return "", ErrOAuthIdentity
		}
	default:
		return "", ErrOAuthIdentity
	}
	if !validateOAuthIdentifier(id) {
		return "", ErrOAuthIdentity
	}
	return id, nil
}
