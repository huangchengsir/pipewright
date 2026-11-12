# Database deployment and migration

Pipewright keeps one active database per instance. Standard builds include SQLite, MySQL, PostgreSQL and Microsoft SQL Server. DM8 needs a private build using your own licensed official driver; see [the build instructions](database-dm-build.md). Vendor source and binaries containing it must stay outside public repositories and release artifacts.

## Connection configuration

`PIPEWRIGHT_DB_DRIVER` selects `sqlite` (default), `mysql`, `postgres`, `sqlserver` or `dm`. SQLite uses `PIPEWRIGHT_DB` as its file path. Server engines use `PIPEWRIGHT_DB_DSN`. Keep connection credentials in your deployment secret configuration, not in command history, checked-in Compose files or support logs.

Examples below contain placeholders, not usable credentials:

| Engine | Connection format | Requirements |
| --- | --- | --- |
| SQLite | `PIPEWRIGHT_DB=/persistent/pipewright.db` | Regular local persistent file; migration targets must be a new file in an existing directory. URI/in-memory SQLite connections can run the existing application but are not eligible migration endpoints. |
| MySQL | `user:password@tcp(host:3306)/database?parseTime=true` | Dedicated MySQL database; driver options follow go-sql-driver/mysql. |
| PostgreSQL | `postgres://user:password@host:5432/database?search_path=public` | PostgreSQL 16 or later; dedicated empty schema. Specify TLS policy appropriate to your deployment. |
| SQL Server | `sqlserver://user:password@host:1433?database=pipewright` | SQL Server 2019, 2022 or 2025; dedicated database/schema. Explicitly configure encryption and certificate trust for your deployment. |
| DM8 | `dm://user:password@host:5236?schema=PIPEWRIGHT` | Private official-driver build, standard compatibility mode and case-sensitive Unicode with empty strings distinct from NULL. |

URL passwords must be percent-encoded. The settings form accepts host, port, database/schema, username and password separately and encodes these fields; an expandable advanced field accepts a complete driver DSN. Driver defaults apply to TLS and other omitted options. Use the advanced field when those options need to be explicit.

The application needs schema creation/migration privileges as well as normal read/write access. SQL Server uses explicit delete triggers to retain multi-path cascade behavior and filtered unique indexes to retain nullable uniqueness. DM requires access to the standard `DBMS_LOCK` package; Pipewright does not create system packages or change database-wide compatibility, memory or security settings. A failed or interrupted DM DDL initialization may leave objects behind: inspect and retain that target, then choose a different dedicated empty target rather than replaying unknown DDL.

## Switch this instance

1. Back up the source database, original vault master key and persistent file stores. Keep the source available throughout the maintenance window. Prepare an empty dedicated target and preserve the same `PIPEWRIGHT_DATA_DIR`.
2. Open **Settings → System → Database and migration**. Select a different engine and enter the target connection. The current engine version and database identity are shown without connection passwords.
3. Run **Check target**. This reads target metadata without creating tables. It verifies source/target identity, encrypted credentials and referenced artifact files. PostgreSQL/SQL Server catalog permissions are checked where supported; actual DDL/write capability is established during initialization, not promised by a connection check.
4. Choose the configuration handoff. **Persistent managed configuration** stores the target profile encrypted under the original master key in the data directory and overrides database environment variables after activation. Otherwise, update your service/container environment yourself and restart when prompted. Restarting a running Compose container does not apply changed environment variables: recreate that container with the same persistent mounts.
5. Confirm the maintenance window. New work and configuration writes stop entering; existing runs, reservations, terminals, guards and unresolved operations remain visible and must finish or be resolved. A timeout never proves they have finished. Existing completion/cancellation paths remain available.
6. The old business process exits/execs before copying. The recovery process holds the exclusive local control lock and exposes only recovery APIs. If needed, sign in on `/database-recovery` with the existing administrator password; this authentication does not create or refresh source database sessions. Copying starts only after an explicit **Continue verification and copy** action.
7. Inspect verified table counts, then confirm activation. The target is checked again before the application restarts. New business writes start only after the new process verifies the exact profile, identity, data, encrypted credentials, artifact references and activation receipt. Previous browser sessions and temporary login flows expire; sign in again normally.

An external restart may be required when in-process exec is unavailable. Keep the original data directory, key, artifacts, repository cache and build cache mounted at the recorded paths. The process lock remains held until successful exec or actual process exit; do not remove its file to bypass an active owner.

### Rotate database credentials after a switch

Stop the instance before rotating a server database password. Keep the same database, schema, server identity, persistent paths and master key. Set `PIPEWRIGHT_DB_DRIVER` to the currently active engine and provide its replacement `PIPEWRIGHT_DB_DSN`, then restart. For persistent managed configuration, also set the one-time `PIPEWRIGHT_DB_CREDENTIAL_ROTATION=1` flag for this restart. Without this flag, managed configuration continues to override even a same-engine environment left over from an earlier migration. With the flag, the supplied same-engine server profile is checked against the recorded actual database identity and saved encrypted only after verification. A wrong database or failed authentication blocks startup; the application does not fall back to the old credentials. Remove the one-time flag after successful startup; future managed restarts use the saved replacement credentials. SQLite paths cannot be overridden through this flag, and a flag with a mismatched engine or during an unfinished migration blocks startup. Environment-only deployments do not need the flag.

A changed connection invalidates an existing prepared preflight; check the target again before starting maintenance. Do not rotate connection settings during draining, copying or activation: those phases remain bound to their exact original profiles. Completed migration history and activation receipts are retained when credentials change.

## Copied data and retained files

The migration manifest currently contains 106 business tables, including credentials, OAuth and login settings, projects, pipelines, runs, steps, logs, retries, configuration drafts, grants, audits and update history. Copies use explicit columns and preserve NULL versus empty values, Unicode, signed 64-bit integers and binary ciphertext. Target constraints and independent row/count/digest checks reject incompatible data rather than silently converting or merging it.

`schema_migrations`, `database_identity` and `database_migration_batches` are local target metadata, not copied business tables. The target gets its own random identity and migration receipts. Physical column order is irrelevant; missing, unknown or duplicate columns fail validation.

Files are not uploaded or copied to a new storage service. Existing content-addressed artifact paths, repository cache and build cache locations remain pinned to the same instance paths. Referenced stored artifacts must exist with the expected size/hash, including objects retained only in encrypted retry checkpoints without a run-artifact row. SQL log rows are copied as database data. Preserve external log destinations and other deployment mounts independently.

## Recovery and rollback boundaries

The encrypted journal, recovery authentication and active profile live under `<PIPEWRIGHT_DATA_DIR>/database-migration`, outside both databases. They require the original vault master key and private filesystem permissions. Do not delete, replace or edit these files to force a phase change. A corrupt journal, wrong key, mismatched database identity or missing referenced data keeps business startup closed.

The same path does not imply the same file identity. Copying or recreating process lock files, or replacing a SQLite database file at its old pathname, is outside this first release's same-instance engine-switch scope. Preserve the actual persistent volume and file identities. Backups remain necessary, but an arbitrary copied data directory is not promised to start directly. Do not delete control directories or forge owner-exit records to bypass recovery checks.

An interrupted copy is resumed explicitly from `/database-recovery`. Committed batch receipts are inspected and verified before any further batch is written. An unknown commit or local persistence result is not a reason to repeat a write blindly. **Verify activation and continue** reconciles authenticated configuration and the transactional activation receipt; it does not append another audit record or invalidate sessions a second time without checking the receipt.

Before business writes are released, **Return to source** verifies the original source and retains the target for investigation. If the deployment environment has already changed to the target, restore the source environment and restart as instructed; environment-only deployments cannot be rewritten by the application. Once the switch is confirmed and target business writes are possible, returning to the old source requires a new reverse migration. Pointing the service directly at the old source would lose new target data and is not supported as rollback.

Completed/cancelled journal history remains readable across future schema upgrades. An old prepared preflight may be discarded or replaced; an unfinished copy must use the exact manifest version that began it.

## Version and test coverage

Real isolated integration verification covered PostgreSQL 18.6, MySQL 8.4.13, SQL Server 2019 (15.0.4510.2), 2022 (16.0.4315.2), 2025 (17.0.5045.3), and DM8 server build 20260707 with private official Go driver 8.1.5.60. PostgreSQL 16/17 are accepted by the compatibility guard but have not received the same real-server validation.

All 20 distinct engine-to-engine copy directions were exercised using the 106-table manifest with data in 26 representative tables, including deep run/retry relationships, encrypted secrets, sessions, OAuth, non-BMP Unicode, NULL/empty values, integer boundaries and stored artifact hashes. This is not a claim that every table has every possible business fixture or that all deployment/restart modes were tested. Whole-application restart and browser acceptance are separate checks.
