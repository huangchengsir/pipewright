package vault

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	secretmask "github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestRegisterMaskSecretsBoundedContextAndNoUsage(t *testing.T) {
	db := testDB(t)
	v := New(db, testKey())
	credential, err := v.Create(CreateInput{Name: "mask fixture", Type: TypeRegistry, Secret: "fixture-user:fixture-secret-123"})
	if err != nil {
		t.Fatal(err)
	}
	m := secretmask.NewMasker()
	if err := RegisterMaskSecrets(context.Background(), v, m); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Scrub("fixture-secret-123"), "fixture-secret-123") {
		t.Fatal("registry password component leaked")
	}
	var used any
	if err := db.QueryRow(`SELECT last_used_at FROM credentials WHERE id = ?`, credential.ID).Scan(&used); err != nil || used != nil {
		t.Fatal("mask registration counted as credential use", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := RegisterMaskSecrets(ctx, v, secretmask.NewMasker()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("vault read did not honor deadline", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("vault masking exceeded bound")
	}
}

func TestRegisterMaskSecretsFailsClosed(t *testing.T) {
	for _, kind := range []string{"unconfigured", "corrupt", "large", "too_many"} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			v := New(db, testKey())
			if kind == "unconfigured" {
				v = New(db, nil)
			} else {
				secret := "fixture-secret-123"
				if kind == "large" {
					secret = strings.Repeat("x", 65536)
				}
				credential, err := v.Create(CreateInput{Name: "fixture", Type: TypeGitToken, Secret: secret})
				if kind == "large" && storetest.Dialect() == store.MySQL {
					// MySQL BLOB rejects a value this large before it can reach the
					// per-value registration guard. Assert the actual storage boundary;
					// SQLite below exercises the guard on a persistable oversized row.
					var mysqlErr *mysql.MySQLError
					if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1406 {
						t.Fatalf("expected MySQL oversized-value rejection, got %v", err)
					}
					if strings.Contains(err.Error(), secret) {
						t.Fatal("oversized secret leaked in storage error")
					}
					var count int
					if err := db.QueryRow(`SELECT COUNT(*) FROM credentials`).Scan(&count); err != nil || count != 0 {
						t.Fatalf("rejected secret persisted: count=%d err=%v", count, err)
					}
					if err := RegisterMaskSecrets(context.Background(), v, secretmask.NewMasker()); err != nil {
						t.Fatal("rejected insert left an unsafe vault state", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if kind == "corrupt" {
					if _, err := db.Exec(`UPDATE credentials SET ciphertext = X'00' WHERE id = ?`, credential.ID); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "too_many" {
					for i := 0; i < 256; i++ {
						if _, err := v.Create(CreateInput{Name: "fixture", Type: TypeGitToken, Secret: secret}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err := RegisterMaskSecrets(context.Background(), v, secretmask.NewMasker()); err == nil {
				t.Fatal("unsafe masking source accepted")
			}
		})
	}
}
