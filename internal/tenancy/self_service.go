package tenancy

import (
	"context"
	"encoding/hex"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"time"
)

type SessionView struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt"`
	ExpiresAt  string `json:"expiresAt"`
	Current    bool   `json:"current"`
	Source     string `json:"source"`
}

func (a *Accounts) Sessions(ctx context.Context, actor Actor) ([]SessionView, error) {
	out := []SessionView{}
	err := configstate.WithReadTx(ctx, a.db, func(q configstate.DBTX) error {
		if _, err := verifyActor(ctx, q, actor, false); err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `SELECT token_hash,created_at,last_seen_at,expires_at,auth_source FROM access_sessions WHERE user_id=? AND mode_epoch=? AND security_generation=? AND expires_at>? ORDER BY created_at DESC,token_hash`+store.Limit(store.DialectOf(a.db), "200"), actor.UserID, actor.ModeEpoch, actor.SecurityGeneration, a.now().Unix())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v SessionView
			var created, seen, expires int64
			if err = rows.Scan(&v.ID, &created, &seen, &expires, &v.Source); err != nil {
				return err
			}
			v.Current = v.ID == actor.SessionHash
			v.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
			v.LastSeenAt = time.Unix(seen, 0).UTC().Format(time.RFC3339)
			v.ExpiresAt = time.Unix(expires, 0).UTC().Format(time.RFC3339)
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}
func (a *Accounts) RevokeSession(ctx context.Context, actor Actor, id string) error {
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 32 {
		return ErrInvalidInput
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return err
	}
	if _, err = verifyActor(ctx, tx, actor, false); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM access_sessions WHERE user_id=? AND token_hash=?`, actor.UserID, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrForbidden
	}
	return tx.Commit()
}

type UserPage struct {
	Users      []User `json:"users"`
	NextCursor string `json:"nextCursor"`
}

// Users is instance-admin only, including disabled and unassigned accounts.
// Team managers use the rate-limited exact-match lookup, never this directory.
func (a *Accounts) Users(ctx context.Context, actor Actor, after string) (UserPage, error) {
	out := UserPage{Users: []User{}}
	if len(after) > 64 {
		return out, ErrInvalidInput
	}
	err := configstate.WithReadTx(ctx, a.db, func(q configstate.DBTX) error {
		if _, err := verifyActor(ctx, q, actor, true); err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `SELECT id,username,display_name,enabled,instance_admin,must_change_password FROM access_users WHERE id>? ORDER BY id`+store.Limit(store.DialectOf(a.db), "51"), after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var user User
			var enabled, admin, must int
			if err = rows.Scan(&user.ID, &user.Username, &user.DisplayName, &enabled, &admin, &must); err != nil {
				return err
			}
			user.Enabled = enabled == 1
			user.InstanceAdmin = admin == 1
			user.MustChangePassword = must == 1
			out.Users = append(out.Users, user)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(out.Users) > 50 {
			out.Users = out.Users[:50]
			out.NextCursor = out.Users[49].ID
		}
		return nil
	})
	return out, err
}
