package auth_test

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/store"
	"path/filepath"
	"testing"
)

func TestRecoveryPasswordNeverWritesSessionsOrAdministrator(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	service := auth.NewService(s.DB, nil)
	if e = service.Bootstrap("admin", "recovery-password"); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"sessions", "admin_user"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, e = s.DB.Exec("CREATE TRIGGER deny_" + table + "_" + op + " BEFORE " + op + " ON " + table + " BEGIN SELECT RAISE(FAIL,'read-only recovery'); END"); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = service.VerifyRecoveryPassword(context.Background(), "admin", "recovery-password"); e != nil {
		t.Fatal(e)
	}
	if e = service.VerifyRecoveryPassword(context.Background(), "admin", "wrong"); !errors.Is(e, auth.ErrInvalidCredentials) {
		t.Fatal(e)
	}
	var count int
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); e != nil || count != 0 {
		t.Fatal("source session changed", count, e)
	}
}
