package store

import (
	"io/fs"
	"testing"
	"time"
)

func TestPlatformSigninUpgradePreservesLegacyAndBinaryIdentity(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, MySQL} {
		t.Run(dialect.String(), func(t *testing.T) {
			s := uninitializedUpgradeStore(t, dialect)
			if _, e := s.DB.Exec(s.schemaMigrationsDDL()); e != nil {
				t.Fatal(e)
			}
			fsys, glob := s.migrationFS()
			entries, e := fs.Glob(fsys, glob)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if migrationVersion(entry) >= "0062_" {
					continue
				}
				body, e := fs.ReadFile(fsys, entry)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.applyMigration(migrationVersion(entry), string(body)); e != nil {
					t.Fatal(e)
				}
			}
			now := time.Now().UTC().Format(time.RFC3339)
			later := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
			if _, e = s.DB.Exec(`INSERT INTO admin_user(id,username,password_hash,created_at,updated_at) VALUES(1,'legacy-admin','preserved-hash',?,?)`, now, now); e != nil {
				t.Fatal(e)
			}
			if _, e = s.DB.Exec(`INSERT INTO sessions(token,csrf_token,created_at,expires_at,last_seen_at) VALUES('legacy-token','legacy-csrf',?,?,?)`, now, later, now); e != nil {
				t.Fatal(e)
			}
			if e = s.migrate(); e != nil {
				t.Fatal(e)
			}
			if e = s.migrate(); e != nil {
				t.Fatal("repeat migration", e)
			}
			var username, hash, source string
			var generation int64
			if e = s.DB.QueryRow(`SELECT username,password_hash,security_generation FROM admin_user WHERE id=1`).Scan(&username, &hash, &generation); e != nil || username != "legacy-admin" || hash != "preserved-hash" || generation != 1 {
				t.Fatal("legacy admin changed", e)
			}
			var verified, absolute *string
			if e = s.DB.QueryRow(`SELECT auth_source,password_verified_at,absolute_expires_at FROM sessions WHERE token='legacy-token'`).Scan(&source, &verified, &absolute); e != nil || source != "password" || verified != nil || absolute != nil {
				t.Fatal("legacy session changed", e)
			}
			// The exact old-binary query remains valid after the additive migration.
			var csrf, created, expires, seen string
			if e = s.DB.QueryRow(`SELECT csrf_token,created_at,expires_at,last_seen_at FROM sessions WHERE token='legacy-token'`).Scan(&csrf, &created, &expires, &seen); e != nil || csrf != "legacy-csrf" {
				t.Fatal("old query incompatible", e)
			}
			var count int
			if e = s.DB.QueryRow(`SELECT COUNT(*) FROM login_provider_settings WHERE visible=1 AND enabled=0`).Scan(&count); e != nil || count != 5 {
				t.Fatal("provider defaults", count, e)
			}
			if _, e = s.DB.Exec(`INSERT INTO login_identities(provider,issuer,subject,display_label,identity_generation,bound_at) VALUES('github','https://issuer.example','CaseSensitive ','fixture',1,?)`, now); e != nil {
				t.Fatal(e)
			}
			if e = s.DB.QueryRow(`SELECT COUNT(*) FROM login_identities WHERE subject='CaseSensitive'`).Scan(&count); e != nil || count != 0 {
				t.Fatal("identity trailing space folded", count, e)
			}
			if e = s.DB.QueryRow(`SELECT COUNT(*) FROM login_identities WHERE subject='casesensitive '`).Scan(&count); e != nil || count != 0 {
				t.Fatal("identity case folded", count, e)
			}
		})
	}
}
