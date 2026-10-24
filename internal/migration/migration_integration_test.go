//go:build integration

package migration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestExecute_SuccessVersionsAreLinear(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "edit")
	id1 := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "CREATE TABLE widgets (id int)")
	if _, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, id1); err != nil {
		t.Fatal(err)
	}
	id2 := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "CREATE TABLE gadgets (id int)")
	if _, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, id2); err != nil {
		t.Fatal(err)
	}

	if !tableExists(t, fx.target, "widgets") || !tableExists(t, fx.target, "gadgets") {
		t.Fatal("expected both tables on the target database")
	}
	assertVersions(t, fx.platform, fx.dbID, 1, 2)
	var applied int
	if err := fx.target.QueryRow(context.Background(), `SELECT count(*) FROM _dbmp.applied_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("applied rows = %d, want 2", applied)
	}
}

func TestExecute_RollsBackEarlierStatement(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "edit")
	id := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID,
		"CREATE TABLE t1 (id int); INSERT INTO no_such_table (id) VALUES (1)")
	res, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed || res.Message == "" {
		t.Fatalf("result = %+v, want failed with a message", res)
	}
	if tableExists(t, fx.target, "t1") {
		t.Fatal("t1 exists after a failed migration")
	}
	status, version, msg := migrationRow(t, fx.platform, fx.dbID, id)
	if status != "failed" || version != nil || msg == nil || *msg == "" {
		t.Fatalf("status=%s version=%v msg=%v", status, version, msg)
	}
	var runs int
	if err := fx.platform.QueryRow(context.Background(), `SELECT count(*) FROM migration_runs WHERE migration_id = $1 AND status = 'failed'`, id).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("failed runs = %d, want 1", runs)
	}
}

func TestExecute_ViewOnlyForbidden(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "view")
	id := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "CREATE TABLE secret_table (id int)")
	resp := fx.srv.PostAs(t, fx.editor, "/db/"+itoa(fx.dbID)+"/migrations/"+itoa(id)+"/execute", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if tableExists(t, fx.target, "secret_table") {
		t.Fatal("view user changed the target database")
	}
}

func TestList_NoGrantForbidden(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "edit")
	stranger := insertUser(t, fx.platform, "stranger")
	resp := fx.srv.GetAs(t, stranger, "/db/"+itoa(fx.dbID)+"/migrations")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403 (%s)", resp.StatusCode, body)
	}
}

func TestExecute_ParallelVersionsAreUnique(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "edit")
	id1 := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "CREATE TABLE alpha (id int)")
	id2 := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "CREATE TABLE beta (id int)")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, id1)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, id2)
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertVersions(t, fx.platform, fx.dbID, 1, 2)
	if !tableExists(t, fx.target, "alpha") || !tableExists(t, fx.target, "beta") {
		t.Fatal("parallel execute did not create both tables")
	}
}

func TestUpdate_SuccessIsRejected(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "edit")
	id := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "CREATE TABLE kept (id int)")
	if _, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, id); err != nil {
		t.Fatal(err)
	}
	form := url.Values{}
	form.Set("title", "diubah")
	form.Set("sql_text", "DROP TABLE kept")
	resp := fx.srv.PostAs(t, fx.editor, "/db/"+itoa(fx.dbID)+"/migrations/"+itoa(id)+"/edit", form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, body)
	}
	status, version, _ := migrationRow(t, fx.platform, fx.dbID, id)
	if status != "success" || version == nil || *version != 1 {
		t.Fatalf("status=%s version=%v", status, version)
	}
}

func TestShow_WrongDatabaseIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "edit")
	otherName, _ := testutil.NewTargetDB(t, "")
	host, port, user, pass := testEndpoint(t)
	otherID := insertConnection(t, fx.platform, fx.srv.Secret, fx.editor.UserID, "other-db", host, port, otherName, user, pass)
	grant(t, fx.platform, fx.groupID, otherID, "view", fx.editor.UserID)
	id := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "SELECT 1")

	resp := fx.srv.GetAs(t, fx.editor, "/db/"+itoa(otherID)+"/migrations/"+itoa(id))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestExecute_FailedCanBeRetried(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "edit")
	first := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "CREATE TABLE first_ok (id int)")
	if _, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, first); err != nil {
		t.Fatal(err)
	}
	retry := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID,
		"CREATE TABLE rolled (id int); INSERT INTO missing (id) VALUES (1)")
	res, err := fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, retry)
	if err != nil || !res.Failed {
		t.Fatalf("first attempt err=%v result=%+v", err, res)
	}
	if err := fx.srv.Service.UpdateDraft(context.Background(), fx.editor, fx.dbID, retry, migration.CreateInput{
		Title:   "pulih",
		SQLText: "CREATE TABLE recovered (id int)",
	}); err != nil {
		t.Fatal(err)
	}
	res, err = fx.srv.Service.Execute(context.Background(), fx.editor, fx.dbID, retry)
	if err != nil || res.Failed {
		t.Fatalf("retry err=%v result=%+v", err, res)
	}
	if tableExists(t, fx.target, "rolled") {
		t.Fatal("rolled table survived the failed attempt")
	}
	if !tableExists(t, fx.target, "recovered") {
		t.Fatal("retried migration did not create recovered")
	}
	status, version, _ := migrationRow(t, fx.platform, fx.dbID, retry)
	if status != "success" || version == nil || *version != 2 {
		t.Fatalf("status=%s version=%v, want success 2", status, version)
	}
	assertVersions(t, fx.platform, fx.dbID, 1, 2)
}

func TestAccess_ViewUserReadsButCannotMutate(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, "view")

	resp := fx.srv.GetAs(t, fx.editor, "/db/"+itoa(fx.dbID)+"/migrations")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("history status = %d, want 200 (%s)", resp.StatusCode, body)
	}

	form := url.Values{}
	form.Set("title", "tidak boleh")
	form.Set("sql_text", "CREATE TABLE nope (id int)")
	resp = fx.srv.PostAs(t, fx.editor, "/db/"+itoa(fx.dbID)+"/migrations", form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create status = %d, want 403", resp.StatusCode)
	}
	var drafts int
	if err := fx.platform.QueryRow(context.Background(), `SELECT count(*) FROM migrations WHERE database_id = $1`, fx.dbID).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 0 {
		t.Fatalf("view user created %d migrations", drafts)
	}

	id := testutil.CreateDraft(t, fx.platform, fx.dbID, fx.editor.UserID, "SELECT 1")
	resp = fx.srv.PostAs(t, fx.editor, "/db/"+itoa(fx.dbID)+"/migrations/"+itoa(id)+"/delete", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete status = %d, want 403", resp.StatusCode)
	}
	status, _, _ := migrationRow(t, fx.platform, fx.dbID, id)
	if status != "draft" {
		t.Fatalf("status = %s, want draft", status)
	}
}

func TestExecute_DeadTargetStaysConsistent(t *testing.T) {
	t.Parallel()
	platform := testutil.NewPlatformDB(t)
	srv := testutil.NewServer(t, platform)
	user := insertUser(t, platform, "alice")
	groupID := insertGroup(t, platform, "dev")
	addMember(t, platform, groupID, user.UserID)
	const secretPass = "s3cret-value"
	dbID := insertConnection(t, platform, srv.Secret, user.UserID, "dead-db", "127.0.0.1", 1, "postgres", "test", secretPass)
	grant(t, platform, groupID, dbID, "edit", user.UserID)
	id := testutil.CreateDraft(t, platform, dbID, user.UserID, "CREATE TABLE should_not (id int)")

	res, err := srv.Service.Execute(context.Background(), user, dbID, id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed {
		t.Fatal("expected a failed attempt")
	}
	if containsSecret(res.Message, secretPass) {
		t.Fatalf("user message leaked the password: %q", res.Message)
	}
	status, _, msg := migrationRow(t, platform, dbID, id)
	if status == "running" {
		t.Fatal("migration left in running")
	}
	if status != "failed" {
		t.Fatalf("status = %s, want failed", status)
	}
	if msg != nil && containsSecret(*msg, secretPass) {
		t.Fatalf("stored error leaked the password: %q", *msg)
	}
}

type fixture struct {
	platform *pgxpool.Pool
	target   *pgxpool.Pool
	srv      *testutil.Server
	dbID     int64
	groupID  int64
	editor   authz.Principal
}

func newFixture(t *testing.T, level string) fixture {
	t.Helper()
	platform := testutil.NewPlatformDB(t)
	targetName, target := testutil.NewTargetDB(t, "")
	srv := testutil.NewServer(t, platform)
	user := insertUser(t, platform, "editor")
	groupID := insertGroup(t, platform, "dev")
	addMember(t, platform, groupID, user.UserID)
	host, port, dbUser, dbPass := testEndpoint(t)
	dbID := insertConnection(t, platform, srv.Secret, user.UserID, "target", host, port, targetName, dbUser, dbPass)
	grant(t, platform, groupID, dbID, level, user.UserID)
	return fixture{platform: platform, target: target, srv: srv, dbID: dbID, groupID: groupID, editor: user}
}

func insertUser(t *testing.T, db platformdb.DBTX, username string) authz.Principal {
	t.Helper()
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = db.QueryRow(context.Background(), `
		INSERT INTO users (username, password_hash, is_admin, is_active)
		VALUES ($1, $2, false, true)
		RETURNING id`, username, hash).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return authz.Principal{UserID: id, Username: username}
}

func insertGroup(t *testing.T, db platformdb.DBTX, name string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(context.Background(), `INSERT INTO groups (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addMember(t *testing.T, db platformdb.DBTX, groupID, userID int64) {
	t.Helper()
	_, err := db.Exec(context.Background(), `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, groupID, userID)
	if err != nil {
		t.Fatal(err)
	}
}

func insertConnection(t *testing.T, pool *pgxpool.Pool, secret []byte, userID int64, name, host string, port int, dbName, user, pass string) int64 {
	t.Helper()
	svc := databases.NewService(pool, databases.NewPgRepo(), nil, secret)
	id, err := svc.Create(context.Background(), databases.CreateInput{
		Name:      name,
		Host:      host,
		Port:      port,
		DBName:    dbName,
		Username:  user,
		Password:  pass,
		SSLMode:   "disable",
		CreatedBy: userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func grant(t *testing.T, pool *pgxpool.Pool, groupID, dbID int64, level string, grantedBy int64) {
	t.Helper()
	err := access.NewService(pool, access.NewPgRepo(), nil).SaveAccess(context.Background(), access.SaveAccessInput{
		GroupID:   groupID,
		Grants:    map[int64]string{dbID: level},
		GrantedBy: grantedBy,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testEndpoint(t *testing.T) (host string, port int, user, pass string) {
	t.Helper()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	host = u.Hostname()
	port, _ = strconv.Atoi(u.Port())
	if port == 0 {
		port = 5432
	}
	user = u.User.Username()
	pass, _ = u.User.Password()
	return host, port, user, pass
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, name).Scan(&ok)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func migrationRow(t *testing.T, pool *pgxpool.Pool, dbID, id int64) (status string, version *int32, message *string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT status, version, error_message
		FROM migrations
		WHERE id = $1 AND database_id = $2`, id, dbID).Scan(&status, &version, &message)
	if err != nil {
		t.Fatal(err)
	}
	return status, version, message
}

func assertVersions(t *testing.T, pool *pgxpool.Pool, dbID int64, want ...int32) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT version FROM migrations
		WHERE database_id = $1 AND status = 'success'
		ORDER BY version`, dbID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int32
	for rows.Next() {
		var v int32
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("versions = %v, want %v", got, want)
		}
	}
}

func containsSecret(msg, secret string) bool {
	return strings.Contains(msg, secret)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
