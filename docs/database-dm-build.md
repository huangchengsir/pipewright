# Private builds with the official DM driver

The standard Pipewright build contains no proprietary DM module and reports DM as unavailable. Obtain the official DM8 Go driver under your own applicable license. The tested package interface is 8.1.5.60; vendor source and binaries containing it must not be added to public source control or public CI/release artifacts.

From the repository root, run one command (the output must be outside the checkout):

```sh
go run ./tools/builddm -driver-dir /private/dm -o /private/bin/pipewright-dm
```

The directory must contain the original `module dm` go.mod. The command uses a temporary Go overlay and separate modfile; it leaves the repository go.mod/go.sum unchanged and does not copy vendor source. Use `-goos linux -goarch amd64` for a Linux build. Build the web assets with the normal release workflow before building the application.

For private integration verification, add `-test-package ./internal/store` and choose a private test output. Run `-test.run TestDMOptionalDriverLOBAdapter` to check the linked official BLOB/CLOB interfaces before connecting to a database. This smoke test does not prove DM server compatibility.

DM connections require standard compatibility mode (`COMPATIBLE_MODE=0` in current memory), distinct NULL/empty text, case sensitivity, and Unicode round trips. Reconnect replay is disabled by the adapter. Do not publish connection DSNs or generated test/build products. Server integration and all migration directions require separate verification.

The database administrator must enable the standard `DBMS_LOCK` package and grant the application access before schema initialization. Pipewright does not run `SP_CREATE_SYSTEM_PACKAGES` or change instance parameters. Its migration lock uses the documented session lock with release-on-commit disabled, because DM DDL implicitly commits. An interrupted DDL version is not automatically replayed: keep the source and inspect the dedicated target.

The explicit DM schema uses character-length VARCHAR declarations and per-table `STORAGE(USING LONG ROW)`; this does not alter global storage parameters. These choices follow the vendor [DDL manual](https://eco.dameng.com/document/dm/zh-cn/pm/definition-statement.html), [transaction manual](https://eco.dameng.com/document/dm/zh-cn/pm/management-affairs.html), and [DBMS_LOCK reference](https://eco.dameng.com/document/dm/zh-cn/pm/dbms_lock-package.html).

## Updating a private DM deployment

Version checks remain available. While the active database is DM, Pipewright refuses public online installation and task resumption: standard release packages and images do not contain the required driver. The decision uses the live database connection, including managed connection profiles, rather than the original environment variable.

For each upgrade, obtain the new Pipewright source, build its web assets, then run the private build command with your licensed driver. Stop the application and replace its executable (or rebuild your private container image) using your normal deployment procedure. Preserve the data directory, original master key, database configuration and file-store mounts. Keep the previous private binary for deployment recovery. Do not replace the application while a database migration is unfinished; complete or cancel that migration first.
