package dbmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// All remote databases/schemas are owned by storetest, never the supplied DSN's
// existing business schema. Its normal cleanup drops only its generated fixture.
// The runner may select exactly one engine and must reject unexpected skips.
func TestNonemptyTargetsLiveContract(t *testing.T) {
	selected := os.Getenv(storetest.DriverEnv)
	count := 0
	for _, d := range []store.Dialect{store.SQLite, store.MySQL, store.PostgreSQL, store.DM, store.SQLServer} {
		if selected != "" && selected != d.String() {
			continue
		}
		count++
		t.Run(d.String(), func(t *testing.T) {
			t.Run("schema_digest_sensitivity", func(t *testing.T) { nonemptySchemaSensitivity(t, d) })
			for _, kind := range []string{"empty_table", "business_rows", "pipewright_data"} {
				t.Run(kind, func(t *testing.T) {
					target := storetest.OpenEmptyForDialect(t, d)
					conn := nonemptyConnection(t, target)
					nonemptySeedTarget(t, target, kind)
					source, current, key := nonemptySource(t, d)
					control, err := AcquireControl(current.Paths.DataDir, &key)
					if err != nil {
						t.Fatal(err)
					}
					defer control.Close()
					runtime := NewRuntime(control, current, source, &key, false, nil)
					defer runtime.Close()
					beforeSource, beforeTarget := nonemptyDigest(t, source), nonemptyDigest(t, target)
					if source.Dialect != d {
						_, err = runtime.Preflight(context.Background(), conn, true)
						if !errors.Is(err, ErrTargetNotEmpty) {
							t.Fatalf("preflight must reject nonempty target: %v", err)
						}
						state, e := control.State()
						if e != nil || state != nil {
							t.Fatal("rejected preflight persisted task", e)
						}
					} else {
						t.Log("SQLite preflight requires external PostgreSQL source; initialization refusal covered locally")
					}
					_, err = InitializeTarget(context.Background(), target, uuid.NewString())
					if !errors.Is(err, ErrTargetNotEmpty) {
						t.Fatalf("initialization must reject nonempty target: %v", err)
					}
					nonemptyUnchanged(t, source, beforeSource, "source")
					nonemptyUnchanged(t, target, beforeTarget, "target")
					// Refusal must leave normal source admission usable, without any leftover
					// migration fence. Check before/after the temporary lease is released.
					release, e := maintenance.Enter(context.Background(), source.DB, "nonempty-refusal-check")
					if e != nil {
						t.Fatal("source admission closed", e)
					}
					release()
					nonemptyUnchanged(t, source, beforeSource, "source after admission")
					t.Log("explicit nonempty refusal; fixture tables/rows and selected source/target schema catalogs unchanged")
				})
			}
			for _, kind := range []string{"business_rows", "other_task"} {
				t.Run("post_preflight_"+kind, func(t *testing.T) { nonemptyAfterPreflight(t, d, kind) })
			}
			t.Run("matching_receipts_and_tampering", func(t *testing.T) { nonemptyResume(t, d) })
		})
	}
	if count == 0 {
		t.Fatal("unsupported database filter")
	}
}

func nonemptySource(t *testing.T, target store.Dialect) (*store.Store, ActiveConfig, [32]byte) {
	t.Helper()
	key := [32]byte{0x53, 0x19}
	d := store.SQLite
	if target == store.SQLite && os.Getenv(storetest.PostgresDSNEnv) != "" {
		d = store.PostgreSQL
	}
	source := storetest.OpenForDialect(t, d)
	conn := nonemptyConnection(t, source)
	cipher, err := vault.New(source.DB, &key).SealSecret([]byte("nonempty-contract-secret"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,?,?,"+store.BinaryPlaceholder(d)+",?,?,?)", "source-credential", "source credential", "git_token", "", cipher, "***", "now", "now")
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.DB.Exec("INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", "source-project", "source project", "https://example.invalid/source", "main", "source-credential", "now", "now")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := Describe(context.Background(), source, conn.DSN)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	paths, err := ResolvePaths(dataDir, filepath.Join(dataDir, "source.db"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return source, ActiveConfig{Connection: conn, Endpoint: endpoint, Paths: paths}, key
}

// Derive only the generated storetest namespace from live metadata. The base
// secret stays in memory and is never printed, persisted or used as the target.
func nonemptyConnection(t *testing.T, db *store.Store) Connection {
	t.Helper()
	ctx := context.Background()
	out := Connection{Driver: db.Dialect.String()}
	if db.Dialect == store.SQLite {
		var seq int
		var name string
		if err := db.DB.QueryRow("PRAGMA database_list").Scan(&seq, &name, &out.DSN); err != nil {
			t.Fatal(err)
		}
		return out
	}
	endpoint, err := Describe(ctx, db, "")
	if err != nil {
		t.Fatal(err)
	}
	switch db.Dialect {
	case store.MySQL:
		cfg, e := mysql.ParseDSN(os.Getenv(storetest.MySQLDSNEnv))
		if e != nil {
			t.Fatal("invalid test MySQL DSN")
		}
		cfg.DBName = endpoint.Database
		out.DSN = cfg.FormatDSN()
	case store.PostgreSQL:
		base := os.Getenv(storetest.PostgresDSNEnv)
		if !strings.HasPrefix(base, "postgres://") && !strings.HasPrefix(base, "postgresql://") {
			out.DSN = base + " search_path=" + endpoint.Schema
			return out
		}
		u, e := url.Parse(base)
		if e != nil {
			t.Fatal("invalid test PostgreSQL URL")
		}
		q := u.Query()
		q.Set("search_path", endpoint.Schema)
		u.RawQuery = q.Encode()
		out.DSN = u.String()
	case store.DM, store.SQLServer:
		env, param, value := storetest.DMDSNEnv, "schema", endpoint.Schema
		if db.Dialect == store.SQLServer {
			env, param, value = storetest.SQLServerDSNEnv, "database", endpoint.Database
		}
		u, e := url.Parse(os.Getenv(env))
		if e != nil {
			t.Fatal("invalid test server URL")
		}
		q := u.Query()
		q.Set(param, value)
		u.RawQuery = q.Encode()
		out.DSN = u.String()
	}
	return out
}
func nonemptySeedTarget(t *testing.T, db *store.Store, kind string) {
	t.Helper()
	if kind == "pipewright_data" {
		if err := db.Migrate(); err != nil {
			t.Fatal(err)
		}
		key := [32]byte{0x14}
		cipher, err := vault.New(db.DB, &key).SealSecret([]byte("existing-target-secret"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,?,?,"+store.BinaryPlaceholder(db.Dialect)+",?,?,?)", "existing-credential", "existing credential", "git_token", "", cipher, "***", "then", "then")
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.DB.Exec("INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", "existing-project", "existing project 😀  ", "https://example.invalid/retained", "main", "existing-credential", "then", "then")
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	textType, blobType := "VARCHAR(200)", "BLOB"
	switch db.Dialect {
	case store.PostgreSQL:
		blobType = "BYTEA"
	case store.SQLServer:
		textType, blobType = "NVARCHAR(200)", "VARBINARY(MAX)"
	}
	_, err := db.DB.Exec("CREATE TABLE existing_business (id INTEGER NOT NULL PRIMARY KEY, label " + textType + " NULL, payload " + blobType + " NULL)")
	if err != nil {
		t.Fatal(err)
	}
	if kind == "empty_table" {
		return
	}
	for _, row := range []struct {
		id    int
		label any
		blob  any
	}{{1, "existing 😀  ", []byte{0, 255, 1}}, {2, "", []byte{}}, {3, nil, nil}} {
		_, err = db.DB.Exec("INSERT INTO existing_business(id,label,payload) VALUES(?,?,"+store.BinaryPlaceholder(db.Dialect)+")", row.id, row.label, row.blob)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Control.Plan is used only for SQLite->SQLite local fallback because the public
// preflight intentionally requires distinct engines. With POSTGRES_DSN set the
// SQLite case also executes the real cross-engine Runtime.Preflight.
func nonemptyPrepared(t *testing.T, c *Control, r *Runtime, target *store.Store, conn Connection) (*State, error) {
	t.Helper()
	if r.Source.Dialect != target.Dialect {
		return r.Preflight(context.Background(), conn, true)
	}
	endpoint := Endpoint{Dialect: "sqlite", FilePath: conn.DSN}
	return c.Plan(r.Current, ActiveConfig{Connection: conn, Endpoint: endpoint, Paths: r.Current.Paths, Managed: true})
}
func nonemptyAfterPreflight(t *testing.T, d store.Dialect, kind string) {
	ctx := context.Background()
	source, current, key := nonemptySource(t, d)
	target := storetest.OpenEmptyForDialect(t, d)
	conn := nonemptyConnection(t, target)
	emptyBefore := nonemptyDigest(t, target)
	// SQLite preflight requires a nonexistent path; retain the owned parent, not
	// an open empty-file artifact. Reopen the same path only for external pollution.
	if d == store.SQLite {
		target.Close()
		if err := os.Remove(conn.DSN); err != nil {
			t.Fatal(err)
		}
	}
	control, err := AcquireControl(current.Paths.DataDir, &key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { control.Close() }()
	runtime := NewRuntime(control, current, source, &key, false, nil)
	sourceBefore := nonemptyDigest(t, source)
	state, err := nonemptyPrepared(t, control, runtime, target, conn)
	if err != nil {
		t.Fatal("empty preflight", err)
	}
	nonemptyUnchanged(t, source, sourceBefore, "source preflight")
	if d == store.SQLite {
		if _, e := os.Stat(conn.DSN); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("preflight created SQLite target", e)
		}
	} else {
		nonemptyUnchanged(t, target, emptyBefore, "empty target preflight")
	}
	if d == store.SQLite {
		target, err = store.Connect(store.OpenConfig{Driver: conn.Driver, DSN: conn.DSN})
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
	}
	if kind == "other_task" {
		if _, err = InitializeTarget(ctx, target, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	} else {
		nonemptySeedTarget(t, target, "business_rows")
	}
	targetBefore := nonemptyDigest(t, target)
	if kind == "business_rows" {
		if _, err = InitializeTarget(ctx, target, state.TaskID); !errors.Is(err, ErrTargetNotEmpty) {
			t.Fatal("polluted initialization accepted", err)
		}
		nonemptyUnchanged(t, target, targetBefore, "polluted initialization")
	}
	// Simulate verified old-process departure without a timer/goroutine: execute
	// the same admission fence and authenticated phase transition, close its lock,
	// and acquire a genuinely new owner before invoking the real ContinueCopy.
	state, err = control.BeginQuiesce(state.TaskID, state.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Drain(ctx, source.DB, state.TaskID); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	control.Close()
	control, err = AcquireControl(current.Paths.DataDir, &key)
	if err != nil {
		t.Fatal(err)
	}
	runtime = NewRuntime(control, current, source, &key, true, nil)
	defer runtime.Close()
	frozenSource := nonemptyDigest(t, source)
	_, err = runtime.ContinueCopy(ctx, state.TaskID, state.Revision)
	want := ErrTargetNotEmpty
	if kind == "other_task" {
		want = ErrMismatch
	}
	if !errors.Is(err, want) {
		t.Fatalf("polluted recovery expected %v, got %v", want, err)
	}
	nonemptyUnchanged(t, source, frozenSource, "frozen source")
	nonemptyUnchanged(t, target, targetBefore, "polluted target")
	state, err = control.State()
	if err != nil || state.Phase != Frozen {
		t.Fatal("refusal did not retain frozen recovery", err)
	}
	if _, err = runtime.Abort(ctx, state.TaskID, state.Revision); err != nil {
		t.Fatal("return to source", err)
	}
	nonemptyUnchanged(t, source, sourceBefore, "source after abort")
	nonemptyUnchanged(t, target, targetBefore, "retained target after abort")
	release, err := maintenance.Enter(ctx, source.DB, "source-restored")
	if err != nil {
		t.Fatal(err)
	}
	release()
	t.Log("post-preflight pollution refused by ContinueCopy; fixture data and selected source/target schema catalog digests retained and source admission restored")
}

func nonemptyResume(t *testing.T, d store.Dialect) {
	ctx := context.Background()
	source, _, _ := nonemptySource(t, d)
	dest := storetest.OpenEmptyForDialect(t, d)
	task := uuid.NewString()
	target, err := InitializeTarget(ctx, dest, task)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore := nonemptyDigest(t, source)
	first, err := CopySnapshot(ctx, source, target)
	if err != nil {
		t.Fatal(err)
	}
	nonemptyUnchanged(t, source, sourceBefore, "source after first copy")
	destBefore := nonemptyDigest(t, dest)
	if _, err = ResumeTarget(ctx, dest, uuid.NewString(), target.Identity()); !errors.Is(err, ErrMismatch) {
		t.Fatal("foreign task receipt accepted", err)
	}
	if _, err = ResumeTarget(ctx, dest, task, uuid.NewString()); !errors.Is(err, ErrIdentity) {
		t.Fatal("foreign identity accepted", err)
	}
	nonemptyUnchanged(t, source, sourceBefore, "source after wrong receipt")
	nonemptyUnchanged(t, dest, destBefore, "target after wrong receipt")
	resumed, err := ResumeTarget(ctx, dest, task, target.Identity())
	if err != nil {
		t.Fatal(err)
	}
	second, err := CopySnapshot(ctx, source, resumed)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("same-task resume", err)
	}
	nonemptyUnchanged(t, source, sourceBefore, "source after resume")
	nonemptyUnchanged(t, dest, destBefore, "target after resume")
	if _, err = dest.DB.Exec("UPDATE credentials SET name=? WHERE id=?", "external tamper", "source-credential"); err != nil {
		t.Fatal(err)
	}
	tampered := nonemptyDigest(t, dest)
	if _, err = CopySnapshot(ctx, source, resumed); !errors.Is(err, ErrMismatch) {
		t.Fatal("tampered row accepted", err)
	}
	nonemptyUnchanged(t, source, sourceBefore, "source after row tamper")
	nonemptyUnchanged(t, dest, tampered, "tampered target not repaired")
	if _, err = dest.DB.Exec("UPDATE database_migration_batches SET content_sha256=? WHERE migration_id=? AND table_name=?", strings.Repeat("0", 64), task, "__initialization__"); err != nil {
		t.Fatal(err)
	}
	tampered = nonemptyDigest(t, dest)
	if _, err = ResumeTarget(ctx, dest, task, target.Identity()); !errors.Is(err, ErrMismatch) {
		t.Fatal("tampered receipt accepted", err)
	}
	nonemptyUnchanged(t, source, sourceBefore, "source after receipt tamper")
	nonemptyUnchanged(t, dest, tampered, "tampered receipt not repaired")
	t.Logf("same-task %d-table resume made zero database changes; foreign task/identity and row/receipt tampering rejected", len(first.Tables))
}

// The independent digest includes every actual table (including local metadata),
// typed SQL values and schema catalogs documented in nonemptyCatalogDigests. It does
// not reuse migration copy/hash code and compares within one engine, so no
// cross-engine type coercion can hide a modification. No values are logged.
func nonemptyDigest(t *testing.T, db *store.Store) string {
	t.Helper()
	var tableQuery, objectQuery string
	switch db.Dialect {
	case store.SQLite:
		tableQuery = "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"
		objectQuery = "SELECT type,name,tbl_name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'"
	case store.MySQL:
		tableQuery = "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE'"
		objectQuery = "SELECT table_name,table_type FROM information_schema.tables WHERE table_schema=DATABASE() UNION ALL SELECT routine_name,routine_type FROM information_schema.routines WHERE routine_schema=DATABASE() UNION ALL SELECT trigger_name,'TRIGGER' FROM information_schema.triggers WHERE trigger_schema=DATABASE()"
	case store.PostgreSQL:
		tableQuery = "SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'"
		objectQuery = "SELECT c.relname,c.relkind::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() UNION ALL SELECT p.proname,'FUNCTION' FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema()"
	case store.DM:
		tableQuery = "SELECT TABLE_NAME FROM ALL_TABLES WHERE OWNER=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())"
		objectQuery = "SELECT OBJECT_NAME,OBJECT_TYPE FROM ALL_OBJECTS WHERE OWNER=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())"
	case store.SQLServer:
		tableQuery = "SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=SCHEMA_NAME() AND TABLE_TYPE='BASE TABLE'"
		objectQuery = "SELECT name,type FROM sys.objects WHERE schema_id=SCHEMA_ID() AND is_ms_shipped=0"
	}
	rows, err := db.DB.Query(tableQuery)
	if err != nil {
		t.Fatal("snapshot table inventory", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if db.Dialect == store.DM {
			name = strings.ToLower(name)
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(tables)
	inventory := map[string]any{"objects": nonemptyQueryDigest(t, db, objectQuery), "catalog": nonemptyCatalogDigests(t, db)}
	for _, table := range tables {
		inventory[table] = nonemptyQueryDigest(t, db, "SELECT * FROM "+store.Identifier(db.Dialect, table))
	}
	body, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func nonemptyQueryDigest(t *testing.T, db *store.Store, query string) string {
	t.Helper()
	rows, err := db.DB.Query(query)
	if err != nil {
		t.Fatal("snapshot query", err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, col := range cols {
		nullable, known := col.Nullable()
		length, hasLength := col.Length()
		types = append(types, fmt.Sprintf("%s/%s/%t/%t/%d/%t", col.Name(), col.DatabaseTypeName(), nullable, known, length, hasLength))
	}
	var values []string
	for rows.Next() {
		v := make([]any, len(cols))
		scan := make([]any, len(v))
		for i := range v {
			scan[i] = &v[i]
		}
		if err = rows.Scan(scan...); err != nil {
			t.Fatal(err)
		}
		typed := make([]any, len(v))
		for i, value := range v {
			// encoding/json alone maps []byte{0,255,1} and string("AP8B") to
			// the same JSON string. Preserve the driver's SQL value type.
			tag := fmt.Sprintf("%T", value)
			if value == nil {
				tag = "SQL_NULL"
			}
			typed[i] = []any{tag, value}
		}
		data, e := json.Marshal(typed)
		if e != nil {
			t.Fatal("snapshot encoding", e)
		}
		values = append(values, string(data))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(values)
	body, err := json.Marshal([]any{types, values})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func nonemptyUnchanged(t *testing.T, db *store.Store, before, label string) {
	t.Helper()
	if after := nonemptyDigest(t, db); after != before {
		t.Fatalf("%s schema/content digest changed: %s -> %s", label, before, after)
	}
}
