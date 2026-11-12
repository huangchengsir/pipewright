package dbmigration

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"time"
)

var ErrRecoveryAuth = errors.New("dbmigration: recovery authentication required")

const RecoverySessionLifetime = time.Hour

type recoverySession struct {
	TaskID    string   `json:"taskId"`
	TokenHash [32]byte `json:"tokenHash"`
	CSRF      string   `json:"csrf"`
	Expires   int64    `json:"expires"`
}

// IssueRecoverySession is called only after administrator authentication. Its
// encrypted, task-bound record survives exec without touching either database.
// A new login revokes the previous recovery token. No token is written in clear.
func (c *Control) IssueRecoverySession(task string, now time.Time) (token, csrf string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, e := c.state()
	if e != nil || s == nil || s.TaskID != task || s.Phase == Completed || s.Phase == Aborted {
		return "", "", ErrRecoveryAuth
	}
	var a, b [32]byte
	if _, e = rand.Read(a[:]); e != nil {
		return "", "", ErrRecoveryAuth
	}
	if _, e = rand.Read(b[:]); e != nil {
		return "", "", ErrRecoveryAuth
	}
	token = base64.RawURLEncoding.EncodeToString(a[:])
	csrf = base64.RawURLEncoding.EncodeToString(b[:])
	record := recoverySession{TaskID: task, TokenHash: sha256.Sum256([]byte(token)), CSRF: csrf, Expires: now.Add(RecoverySessionLifetime).Unix()}
	if e = c.write("recovery", record); e != nil {
		return "", "", e
	}
	return token, csrf, nil
}

// RecoverySession validates the cookie on GET. A mutation must additionally
// compare the returned CSRF value with its explicit request header and reject
// cross-origin requests; neither cookie nor CSRF belongs in URLs or logs.
func (c *Control) RecoverySession(token string, now time.Time) (csrf string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(token) != 43 {
		return "", ErrRecoveryAuth
	}
	raw, e := c.read("recovery")
	if e != nil {
		return "", ErrRecoveryAuth
	}
	defer clear(raw)
	var r recoverySession
	if decodeStrict(raw, &r) != nil || r.Expires <= now.Unix() || r.Expires > now.Add(RecoverySessionLifetime).Unix() {
		return "", ErrRecoveryAuth
	}
	s, e := c.state()
	if e != nil || s == nil || s.TaskID != r.TaskID || s.Phase == Completed || s.Phase == Aborted {
		return "", ErrRecoveryAuth
	}
	got := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(got[:], r.TokenHash[:]) != 1 {
		return "", ErrRecoveryAuth
	}
	return r.CSRF, nil
}
