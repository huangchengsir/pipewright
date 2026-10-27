package store

import (
	"io/fs"
	"testing"
)

func TestOAuthLifecycleUpgradePreservesLegacyCredentialIdentity(t *testing.T) {
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
				version := migrationVersion(entry)
				if version >= "0060_" {
					continue
				}
				body, e := fs.ReadFile(fsys, entry)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.applyMigration(version, string(body)); e != nil {
					t.Fatal(e)
				}
			}
			for _, q := range []string{
				`INSERT INTO credentials(id,name,type,scope,username,ciphertext,masked_value,created_at,updated_at) VALUES('oauth-legacy','gitea:looks-like-a-name','git_token','scope','alice',X'010203','masked','old','old')`,
				`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('oauth-project','legacy','https://fixture.test/repository','main','oauth-legacy','old','old')`,
			} {
				if _, e = s.DB.Exec(q); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.migrate(); e != nil {
				t.Fatal(e)
			}
			if e = s.migrate(); e != nil {
				t.Fatal(e)
			}
			for _, table := range []string{"oauth_credentials", "oauth_credential_uses", "oauth_authorizations"} {
				var n int
				if e = s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil || n != 0 {
					t.Fatal("invented OAuth identity, claim or user", table, n, e)
				}
			}
			var name, scope, username, reference string
			var ciphertext []byte
			if e = s.DB.QueryRow("SELECT name,scope,username,ciphertext FROM credentials WHERE id='oauth-legacy'").Scan(&name, &scope, &username, &ciphertext); e != nil {
				t.Fatal(e)
			}
			if e = s.DB.QueryRow("SELECT credential_id FROM projects WHERE id='oauth-project'").Scan(&reference); e != nil {
				t.Fatal(e)
			}
			if name != "gitea:looks-like-a-name" || scope != "scope" || username != "alice" || string(ciphertext) != string([]byte{1, 2, 3}) || reference != "oauth-legacy" {
				t.Fatal("legacy credential or project reference changed")
			}
		})
	}
}
