# Database Management Platform

A web platform for managing several PostgreSQL databases safely: login and access control, browsing schema and data, then writing and running migrations inside a transaction.

The backend is Go with server-rendered `html/template` pages. For this milestone the only target database is PostgreSQL.

## Running

Requires Go 1.24+, Docker Desktop, and `make` (Git Bash). Leave the ports below free of a local Postgres install.

```bash
cp .env.example .env
# set APP_SECRET_KEY (openssl rand -base64 32) and BOOTSTRAP_ADMIN_PASSWORD
make up
make run          # http://localhost:8080
make seed         # demo users, groups, connections, and grants
```

`make run` applies platform migrations automatically. If a new migration file has a lower number than one already applied, run `make db-reset`.

| Command                 | Purpose                                                              |
|-------------------------|----------------------------------------------------------------------|
| `make up` / `make down` | Start / stop Postgres                                                |
| `make db-reset`         | Drop volumes and start clean                                         |
| `make test`             | Unit tests                                                           |
| `make test-integration` | Unit and integration tests (needs `make up` and `TEST_DATABASE_URL`) |
| `make check`            | `lint` plus integration tests. Must pass before opening a PR         |

Local ports: app `8080`, platform-db `5432`, target-db-1 `5433`, target-db-2 `5434`, test-db `5440`.

## Demo accounts

On the first `make run`, while the user table is empty, the admin is created from `BOOTSTRAP_ADMIN_USERNAME` / `BOOTSTRAP_ADMIN_PASSWORD`.

`make seed` adds:

| User  | Password      | Group           |
|-------|---------------|-----------------|
| alice | `password123` | dev             |
| bob   | `password123` | dev and analyst |
| carol | `password123` | analyst         |

Connections `target-db-1` (port 5433, database `target1`) and `target-db-2` (port 5434, database `target2`) use user/password `target`. Grants: `dev` has edit on db1; `analyst` has view on db1 and db2. Bob is in both groups, so Bob's effective access to db1 is edit.

The target connection password in the seed is for the local demo only.

## Design decisions

| #   | Decision                                                                                                                                                                                        |
|-----|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| D1  | Effective access is the highest level from all of the user's groups on that database (`edit` > `view` > none).                                                                                  |
| D2  | Platform Admin does not automatically get database access. The admin must belong to a group that has a grant.                                                                                   |
| D3  | Session: 30 minute idle, 8 hour absolute, 15 minute rotation, 30 second grace. All of these can be changed through env.                                                                         |
| D4  | The session token is 32 random bytes. The database stores only its SHA-256 hash.                                                                                                                |
| D5  | A request that presents the previous token during the grace period is served, and that token is not rotated again.                                                                              |
| D6  | A privilege increase (becoming admin, joining a group, or a grant rising) increments `privilege_version`. The session rotates on the next request.                                              |
| D7  | Deactivating a user or removing admin rights revokes all of that user's sessions. Leaving a group or losing a grant takes effect immediately because authz reads the database on every request. |
| D8  | Users are not deleted permanently. Deactivation sets `is_active = false`.                                                                                                                       |
| D9  | A connection that already has migration history cannot be deleted (`ON DELETE RESTRICT`).                                                                                                       |
| D10 | The version number is assigned on successful execution: `max(version) + 1` per database. Drafts and failures have no version yet. Every attempt is recorded in `migration_runs`.                |
| D11 | A `success` migration cannot be edited, deleted, or run again. A draft can be edited, deleted, and run. A `failed` migration can be edited and run again, and cannot be deleted.                |
| D12 | Executions for one database are serialized with `SELECT ... FOR UPDATE` on the `db_connections` row.                                                                                            |
| D13 | SQL containing `BEGIN` / `COMMIT` / `ROLLBACK` / `SAVEPOINT`, `CREATE INDEX CONCURRENTLY`, `VACUUM`, or `CREATE DATABASE` is rejected. The same words inside a string or a comment are allowed. |
| D14 | The database explorer is read-only: a `READ ONLY` transaction, `statement_timeout`, quoted identifiers, and paginated data.                                                                     |
| D15 | A request with no login is redirected to `/login`. A logged-in user without access gets 403.                                                                                                    |

### Session parameters

| Env                         | Default |
|-----------------------------|---------|
| `SESSION_IDLE_TIMEOUT`      | `30m`   |
| `SESSION_ABSOLUTE_TIMEOUT`  | `8h`    |
| `SESSION_ROTATION_INTERVAL` | `15m`   |
| `SESSION_GRACE_PERIOD`      | `30s`   |

Session cookie: `dbmp_session` (`HttpOnly`, `SameSite=Lax`, `Secure` when `APP_ENV=production`).

### Permission matrix

Platform Admin is not a database access level. `/db/...` routes follow the group grant.

| Route                                           | Minimum access   |
|-------------------------------------------------|------------------|
| `GET/POST /login`, `POST /logout`               | public / session |
| `GET /`                                         | login            |
| `/admin/users`, `/admin/groups`                 | platform admin   |
| `/admin/databases`, `/admin/groups/{id}/access` | platform admin   |
| `GET /db/{dbID}/...` (schema and data)          | view             |
| `GET /db/{dbID}/migrations`, `GET .../{id}`     | view             |
| create, edit, execute, delete a draft, analyze  | edit             |

Every state-changing POST carries a CSRF token. Hiding a button in the UI is not authorization.

## Migration

History is linear per database: versions 1, 2, 3, ... with no gaps. A new version is written only when the whole script commits.

Execution:

1. Lock the connection row in the platform database.
2. Reject the attempt when the status is not `draft` or `failed`, or when the SQL breaks D13.
3. Run the script in a target transaction with a 60 second `statement_timeout` and a 5 second `lock_timeout`. The simple query protocol is used so one field can hold several statements.
4. In that same transaction, record the version in `_dbmp.applied_migrations` on the target database.
5. Commit the target, then write the version, status, and one `migration_runs` row on the platform. If any statement fails, the target transaction rolls back (earlier statements disappear too) and the status becomes `failed`.

If the process dies before the platform commit, the status stays `draft` or `failed` because the `running` change was never committed. If the target has already committed and the platform update fails, the `_dbmp.applied_migrations` row remains and can be used for reconciliation. The target side is atomic on purpose.

Postgres errors from the user's own SQL (for example, a relation already exists) are shown. A connection-failure message does not include the password or the DSN.

The **Analisis** button on a draft or failed migration sends `POST /db/{dbID}/migrations/{id}/explain`, with `analyze=1` when the "Dengan ANALYZE" checkbox is checked. The route and the engine live in package `explain`. The detail page includes the `explain_plan` partial only when plan data is actually sent, so the page still renders before that partial exists.

EXPLAIN limits: `nextval()` is not rolled back, and `EXPLAIN ANALYZE` holds locks while it runs. That is why this action requires edit access.

## Layout

```
cmd/server          wiring
cmd/seed            demo data
internal/auth       login and passwords             (A)
internal/session    session                         (A)
internal/csrf       CSRF                            (A)
internal/authz      policy and effective access     (A)
internal/admin      users, groups, databases, access
internal/dbconn     connections, encryption, pool   (B)
internal/explorer   schema browser                  (B)
internal/explain    EXPLAIN                         (B)
internal/migration  editor, execution, history      (C)
internal/config platformdb apperr clock httpx view testutil
db/platform_migrations
web/templates web/static
```

## Work split

| Letter | NIM      | Name                  | Responsibility                                                                                                      |
|--------|----------|-----------------------|---------------------------------------------------------------------------------------------------------------------|
| A      | 13524XXX |                       | identity, session, CSRF, authz, users, and groups.                                                                  |
| B      | 13524XXX |                       | database connections, grants, the schema/data explorer, and the EXPLAIN engine.                                     |
| C      | 13524011 | Muhammad Iqbal Raihan | platform infra, shared templates, test helpers, and migration (including the SQL splitter and the Analisis button). |

The MySQL/MariaDB bonus is out of scope: MySQL DDL does an implicit commit, so the migration rollback guarantee would not hold. Schema diff is not part of this milestone.

### Session (A)

Session middleware (idle, absolute, rotation, grace) and CSRF will land in packages `session` and `csrf`. `main` already has a place to mount them. Until they are mounted, protected routes reject a request that has no principal.

### Explorer and EXPLAIN (B)

The schema/data browser and the plan parser will land in packages `explorer` and `explain`. The statement-splitting contract is `migration.SplitStatements`. The plan partial expected by the detail page is the `explain_plan` template.
