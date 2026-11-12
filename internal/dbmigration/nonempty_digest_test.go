package dbmigration

import (
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

// Fixed, read-only catalogs, deliberately excluding row counts, physical page
// locations, statistics and modification timestamps. This is a semantic schema
// inventory for this fixture, not a portable backup or complete server settings
// dump. Encrypted/hidden routine bodies and engine storage/partition attributes
// outside the selected catalogs are not claimed as complete DDL coverage.
// DM's documented SYS catalogs cover key order, uniqueness, constraint text and
// defaults. DM has no per-column collation field in SYSCOLUMNS; database case and
// empty-string behavior is sampled instead. Routine/trigger text uses ALL_SOURCE
// and ALL_TRIGGERS. See DM8 admin manual appendix 1 (SYSINDEXES/SYSCOLUMNS/SYSCONS).
func nonemptyCatalogDigests(t *testing.T, db *store.Store) map[string]string {
	t.Helper()
	queries := map[string]string{}
	switch db.Dialect {
	case store.SQLite:
		queries["columns_constraints_definitions"] = "SELECT type,name,tbl_name,sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'"
		queries["indexes"] = "SELECT m.name AS table_name,l.name,l.[unique],l.origin,l.partial,x.seqno,x.cid,x.name,x.desc,x.coll,x.key FROM sqlite_master m JOIN pragma_index_list(m.name) l JOIN pragma_index_xinfo(l.name) x WHERE m.type='table' AND m.name NOT LIKE 'sqlite_%'"
		queries["constraints"] = "SELECT name,sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"
	case store.MySQL:
		queries["columns"] = "SELECT table_name,column_name,ordinal_position,column_type,is_nullable,column_default,character_set_name,collation_name,extra,generation_expression FROM information_schema.columns WHERE table_schema=DATABASE()"
		queries["indexes"] = "SELECT table_name,index_name,non_unique,seq_in_index,column_name,collation,sub_part,index_type,is_visible,expression FROM information_schema.statistics WHERE table_schema=DATABASE()"
		queries["constraints"] = "SELECT t.table_name,t.constraint_name,t.constraint_type,c.check_clause FROM information_schema.table_constraints t LEFT JOIN information_schema.check_constraints c ON c.constraint_schema=t.constraint_schema AND c.constraint_name=t.constraint_name WHERE t.table_schema=DATABASE()"
		queries["constraint_columns"] = "SELECT table_name,constraint_name,column_name,ordinal_position,position_in_unique_constraint,referenced_table_schema,referenced_table_name,referenced_column_name FROM information_schema.key_column_usage WHERE table_schema=DATABASE()"
		queries["foreign_keys"] = "SELECT table_name,constraint_name,unique_constraint_schema,unique_constraint_name,match_option,update_rule,delete_rule FROM information_schema.referential_constraints WHERE constraint_schema=DATABASE()"
		queries["triggers"] = "SELECT trigger_name,event_manipulation,event_object_table,action_order,action_condition,action_statement,action_orientation,action_timing FROM information_schema.triggers WHERE trigger_schema=DATABASE()"
		queries["routines"] = "SELECT routine_name,routine_type,data_type,routine_definition,is_deterministic,sql_data_access,security_type,sql_mode FROM information_schema.routines WHERE routine_schema=DATABASE()"
		queries["views"] = "SELECT table_name,view_definition,check_option,security_type FROM information_schema.views WHERE table_schema=DATABASE()"
	case store.PostgreSQL:
		queries["columns"] = "SELECT table_name,column_name,ordinal_position,column_default,is_nullable,data_type,udt_schema,udt_name,character_maximum_length,numeric_precision,numeric_scale,collation_schema,collation_name,is_identity,identity_generation,is_generated,generation_expression FROM information_schema.columns WHERE table_schema=current_schema()"
		queries["indexes"] = "SELECT tablename,indexname,indexdef FROM pg_indexes WHERE schemaname=current_schema()"
		queries["constraints"] = "SELECT r.relname,c.conname,c.contype,pg_get_constraintdef(c.oid),c.convalidated,c.condeferrable,c.condeferred FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema()"
		queries["triggers"] = "SELECT r.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled FROM pg_trigger t JOIN pg_class r ON r.oid=t.tgrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema() AND NOT t.tgisinternal"
		queries["routines"] = "SELECT p.proname,pg_get_function_identity_arguments(p.oid),pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND p.prokind IN ('f','p')"
		queries["views"] = "SELECT c.relname,pg_get_viewdef(c.oid) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relkind IN ('v','m')"
	case store.SQLServer:
		queries["columns"] = "SELECT o.name,c.name,c.column_id,ty.name,c.max_length,c.precision,c.scale,c.is_nullable,c.collation_name,c.is_identity,c.is_computed,dc.definition,cc.definition FROM sys.columns c JOIN sys.objects o ON o.object_id=c.object_id JOIN sys.types ty ON ty.user_type_id=c.user_type_id LEFT JOIN sys.default_constraints dc ON dc.object_id=c.default_object_id LEFT JOIN sys.computed_columns cc ON cc.object_id=c.object_id AND cc.column_id=c.column_id WHERE o.schema_id=SCHEMA_ID() AND o.is_ms_shipped=0"
		queries["indexes"] = "SELECT o.name,i.name,i.type_desc,i.is_unique,i.is_primary_key,i.is_unique_constraint,i.is_disabled,i.filter_definition,ic.index_column_id,ic.key_ordinal,ic.is_descending_key,ic.is_included_column,c.name FROM sys.indexes i JOIN sys.objects o ON o.object_id=i.object_id LEFT JOIN sys.index_columns ic ON ic.object_id=i.object_id AND ic.index_id=i.index_id LEFT JOIN sys.columns c ON c.object_id=ic.object_id AND c.column_id=ic.column_id WHERE o.schema_id=SCHEMA_ID() AND o.is_ms_shipped=0"
		queries["constraints"] = "SELECT OBJECT_NAME(parent_object_id),name,definition,is_disabled,is_not_trusted FROM sys.check_constraints WHERE schema_id=SCHEMA_ID()"
		queries["foreign_keys"] = "SELECT f.name,OBJECT_NAME(f.parent_object_id),COL_NAME(fc.parent_object_id,fc.parent_column_id),fc.constraint_column_id,OBJECT_NAME(f.referenced_object_id),COL_NAME(fc.referenced_object_id,fc.referenced_column_id),f.delete_referential_action_desc,f.update_referential_action_desc,f.is_disabled,f.is_not_trusted FROM sys.foreign_keys f JOIN sys.foreign_key_columns fc ON fc.constraint_object_id=f.object_id WHERE f.schema_id=SCHEMA_ID()"
		queries["definitions"] = "SELECT o.name,o.type,m.definition,m.uses_ansi_nulls,m.uses_quoted_identifier FROM sys.sql_modules m JOIN sys.objects o ON o.object_id=m.object_id WHERE o.schema_id=SCHEMA_ID() AND o.is_ms_shipped=0"
		queries["triggers"] = "SELECT t.name,OBJECT_NAME(t.parent_id),t.is_disabled,t.is_instead_of_trigger,OBJECT_DEFINITION(t.object_id) FROM sys.triggers t JOIN sys.objects o ON o.object_id=t.object_id WHERE o.schema_id=SCHEMA_ID() AND o.is_ms_shipped=0"
	case store.DM:
		queries["columns"] = "SELECT o.NAME,c.NAME,c.COLID,c.TYPE$,c.LENGTH$,c.SCALE,c.NULLABLE$,c.DEFVAL FROM SYS.SYSCOLUMNS c JOIN SYS.SYSOBJECTS o ON o.ID=c.ID WHERE o.SCHID=CURRENT_SCHID() AND o.SUBTYPE$='UTAB'"
		queries["indexes"] = "SELECT t.NAME,o.NAME,i.ISUNIQUE,i.TYPE$,i.XTYPE,i.FLAG,i.KEYNUM,i.KEYINFO FROM SYS.SYSINDEXES i JOIN SYS.SYSOBJECTS o ON o.ID=i.ID JOIN SYS.SYSOBJECTS t ON t.ID=o.PID WHERE t.SCHID=CURRENT_SCHID()"
		queries["constraints"] = "SELECT t.NAME,o.NAME,c.TYPE$,c.VALID,c.CHECKINFO,c.FACTION,o.INFO6 FROM SYS.SYSCONS c JOIN SYS.SYSOBJECTS o ON o.ID=c.ID JOIN SYS.SYSOBJECTS t ON t.ID=c.TABLEID WHERE t.SCHID=CURRENT_SCHID()"
		queries["foreign_keys"] = "SELECT TABLE_NAME,CONSTRAINT_NAME,CONSTRAINT_TYPE,R_OWNER,R_CONSTRAINT_NAME,DELETE_RULE,STATUS FROM ALL_CONSTRAINTS WHERE OWNER=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())"
		queries["constraint_columns"] = "SELECT TABLE_NAME,CONSTRAINT_NAME,COLUMN_NAME,POSITION FROM ALL_CONS_COLUMNS WHERE OWNER=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())"
		queries["triggers"] = `SELECT "TRIGGER_NAME","TRIGGERING_TYPE","TRIGGERING_EVENT","TABLE_OWNER","BASE_OBJECT_TYPE","TABLE_NAME","COLUMN_NAME","STATUS","DESCRIPTION","TRIGGER_BODY" FROM ALL_TRIGGERS WHERE "OWNER"=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())`
		queries["source"] = `SELECT "NAME","TYPE","LINE","TEXT" FROM ALL_SOURCE WHERE "OWNER"=SF_GET_SCHEMA_NAME_BY_ID(CURRENT_SCHID())`
		queries["text_semantics"] = "SELECT CASE WHEN 'a'='A' THEN 1 ELSE 0 END,CASE WHEN CAST('' AS VARCHAR(10)) IS NULL THEN 1 ELSE 0 END FROM DUAL"
	}
	result := map[string]string{}
	for name, query := range queries {
		result[name] = nonemptyQueryDigest(t, db, query)
	}
	return result
}

func TestNonemptyDigestPreservesSQLiteStorageTypes(t *testing.T) {
	db := storetest.OpenEmptyForDialect(t, store.SQLite)
	if _, err := db.DB.Exec("CREATE TABLE typed_value(v BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec("INSERT INTO typed_value(v) VALUES(NULL)"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, tc := range []struct {
		name  string
		value any
	}{{"SQL_NULL", nil}, {"empty_blob", []byte{}}, {"empty_text", ""}, {"blob", []byte{0, 255, 1}}, {"base64_looking_text", "AP8B"}} {
		if _, err := db.DB.Exec("UPDATE typed_value SET v=?", tc.value); err != nil {
			t.Fatal(err)
		}
		digest := nonemptyDigest(t, db)
		if prior, ok := seen[digest]; ok {
			t.Fatalf("SQL value types collapsed: %s and %s", prior, tc.name)
		}
		seen[digest] = tc.name
	}
}

func nonemptySchemaSensitivity(t *testing.T, d store.Dialect) {
	t.Helper()
	db := storetest.OpenEmptyForDialect(t, d)
	createTable := func(check string) {
		t.Helper()
		column := "c VARCHAR(32) DEFAULT 'initial'"
		if d == store.SQLServer {
			column = "c NVARCHAR(32) CONSTRAINT probe_default DEFAULT 'initial'"
		}
		query := "CREATE TABLE schema_probe(a INTEGER NOT NULL,b INTEGER NOT NULL," + column + ",CONSTRAINT probe_primary PRIMARY KEY(a),CONSTRAINT probe_check CHECK(a " + check + "))"
		if _, err := db.DB.Exec(query); err != nil {
			t.Fatal("create schema sensitivity fixture", err)
		}
	}
	createIndex := func(def string) {
		t.Helper()
		if _, err := db.DB.Exec("CREATE " + def); err != nil {
			t.Fatal(err)
		}
	}
	dropIndex := func() {
		t.Helper()
		query := "DROP INDEX probe_index"
		if d == store.MySQL || d == store.SQLServer {
			query += " ON schema_probe"
		}
		if _, err := db.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	createTable(">=0")
	before := nonemptyCatalogDigests(t, db)
	createIndex("UNIQUE INDEX probe_index ON schema_probe(b,a)")
	uniqueBA := nonemptyCatalogDigests(t, db)
	if before["indexes"] == uniqueBA["indexes"] {
		t.Fatal("independent unique index absent from catalog digest")
	}
	dropIndex()
	createIndex("UNIQUE INDEX probe_index ON schema_probe(a,b)")
	uniqueAB := nonemptyCatalogDigests(t, db)
	if uniqueAB["indexes"] == uniqueBA["indexes"] {
		t.Fatal("index column order absent from digest")
	}
	dropIndex()
	createIndex("INDEX probe_index ON schema_probe(a,b)")
	plainAB := nonemptyCatalogDigests(t, db)
	if plainAB["indexes"] == uniqueAB["indexes"] {
		t.Fatal("index uniqueness absent from digest")
	}
	// Empty owned fixture can be rebuilt without moving any data. Compare the
	// constraint component itself so an unrelated new object ID cannot pass this.
	if _, err := db.DB.Exec("DROP TABLE schema_probe"); err != nil {
		t.Fatal(err)
	}
	createTable(">=1")
	changed := nonemptyCatalogDigests(t, db)
	if changed["constraints"] == plainAB["constraints"] {
		t.Fatal("check constraint definition absent from digest")
	}
	t.Log("independent index presence, key order, uniqueness and named check definition change all alter their catalog digest")
}
