//go:build integration

package migration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestSubmit_FreezesRevisionAndOpensApproval(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Buat", "CREATE TABLE t (id int)", "DROP TABLE t", false, "")
	exec := submit(t, fx, m.ID)
	if exec.Status != migration.StatusPendingReview || exec.Kind != migration.KindApply {
		t.Fatalf("execution = %+v", exec)
	}
	var leader *int64
	var decision string
	err := fx.platform.QueryRow(context.Background(), `
		SELECT leader_user_id, decision FROM execution_approvals WHERE execution_id = $1`, exec.ID).
		Scan(&leader, &decision)
	if err != nil {
		t.Fatal(err)
	}
	if leader != nil || decision != "pending" {
		t.Fatalf("leader = %v decision = %s", leader, decision)
	}
	if _, err := fx.srv.Service.Submit(context.Background(), fx.editor, fx.dbID, m.ID); !isReason(err, "blocked") {
		t.Fatalf("submit kedua = %v", err)
	}
	if _, err := fx.platform.Exec(context.Background(), `UPDATE executions SET status = 'rejected' WHERE id = $1`, exec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.srv.Service.Submit(context.Background(), fx.editor, fx.dbID, m.ID); !isReason(err, "not_submittable") {
		t.Fatalf("submit setelah ditolak = %v", err)
	}
	got, err := fx.srv.Service.Get(context.Background(), fx.dbID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision.RevisionNo != 1 || got.Revision.FrozenAt == nil || got.Revision.UpSQL != "CREATE TABLE t (id int)" {
		t.Fatalf("revisi berubah: %+v", got.Revision)
	}
}

func TestUpdate_FrozenRevisionCreatesNext(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Buat", "CREATE TABLE t (id int)", "DROP TABLE t", false, "")
	submit(t, fx, m.ID)
	if _, err := fx.srv.Service.Update(context.Background(), fx.editor, fx.dbID, m.ID, migration.Input{
		Title: "Buat", UpSQL: "CREATE TABLE t2 (id int)", DownSQL: "DROP TABLE t2",
	}); !isReason(err, "blocked") {
		t.Fatalf("ubah saat masih review = %v", err)
	}
	if _, err := fx.platform.Exec(context.Background(), `
		UPDATE executions SET status = 'rejected' WHERE migration_id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := fx.srv.Service.Update(context.Background(), fx.editor, fx.dbID, m.ID, migration.Input{
		Title: "Buat", UpSQL: "CREATE TABLE t2 (id int)", DownSQL: "DROP TABLE t2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision.RevisionNo != 2 || updated.Revision.FrozenAt != nil {
		t.Fatalf("revisi baru = %+v", updated.Revision)
	}
	var old string
	if err := fx.platform.QueryRow(context.Background(), `
		SELECT up_sql FROM migration_revisions WHERE migration_id = $1 AND revision_no = 1`, m.ID).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if old != "CREATE TABLE t (id int)" {
		t.Fatalf("revisi 1 berubah: %s", old)
	}
}

func TestChain_RollbackOnlyTopAndForward(t *testing.T) {
	fx := newFixture(t, "edit")
	a := create(t, fx, "A", "CREATE TABLE a (id int)", "DROP TABLE a", false, "")
	b := create(t, fx, "B", "CREATE TABLE b (id int)", "DROP TABLE b", false, "")
	ea := submit(t, fx, a.ID)
	eb := submit(t, fx, b.ID)
	forceSuccess(t, fx.platform, ea.ID, 1)
	forceSuccess(t, fx.platform, eb.ID, 2)

	if _, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, a.ID); !isReason(err, "not_top_of_chain") {
		t.Fatalf("rollback A = %v", err)
	}
	rb, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rb.Kind != migration.KindRollback || rb.RollsBackExecutionID == nil || *rb.RollsBackExecutionID != eb.ID {
		t.Fatalf("rollback = %+v", rb)
	}

	forceSuccess(t, fx.platform, rb.ID, 0)
	clearVersion(t, fx.platform, b.ID)
	fwd, err := fx.srv.Service.Forward(context.Background(), fx.editor, fx.dbID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fwd.Kind != migration.KindForward || fwd.Status != migration.StatusPendingReview {
		t.Fatalf("forward = %+v", fwd)
	}
	if _, err := fx.srv.Service.Forward(context.Background(), fx.editor, fx.dbID, a.ID); !isReason(err, "already_active") {
		t.Fatalf("forward A = %v", err)
	}
}

func TestRollback_IrreversibleRejected(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Hapus", "DROP TABLE t", "", true, "tidak ada cadangan")
	e := submit(t, fx, m.ID)
	forceSuccess(t, fx.platform, e.ID, 1)
	if _, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, m.ID); !isReason(err, "irreversible") {
		t.Fatalf("err = %v", err)
	}
}

func TestForward_AfterFailure(t *testing.T) {
	fx := newFixture(t, "edit")
	m := create(t, fx, "Coba", "CREATE TABLE c (id int)", "DROP TABLE c", false, "")
	e := submit(t, fx, m.ID)
	if _, err := fx.platform.Exec(context.Background(), `UPDATE executions SET status = 'failed' WHERE id = $1`, e.ID); err != nil {
		t.Fatal(err)
	}
	fwd, err := fx.srv.Service.Forward(context.Background(), fx.editor, fx.dbID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fwd.Kind != migration.KindForward {
		t.Fatalf("kind = %s", fwd.Kind)
	}
}

func TestHistory_HideRollback(t *testing.T) {
	fx := newFixture(t, "edit")
	a := create(t, fx, "A", "SELECT 1", "SELECT 1", false, "")
	b := create(t, fx, "B", "SELECT 1", "SELECT 1", false, "")
	c := create(t, fx, "C", "SELECT 1", "SELECT 1", false, "")
	e1 := submit(t, fx, a.ID)
	e2 := submit(t, fx, b.ID)
	forceSuccess(t, fx.platform, e1.ID, 1)
	forceSuccess(t, fx.platform, e2.ID, 2)
	e3, err := fx.srv.Service.Rollback(context.Background(), fx.editor, fx.dbID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	forceSuccess(t, fx.platform, e3.ID, 0)
	clearVersion(t, fx.platform, b.ID)
	e4, err := fx.srv.Service.Forward(context.Background(), fx.editor, fx.dbID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	forceSuccess(t, fx.platform, e4.ID, 2)
	e5 := submit(t, fx, c.ID)
	forceSuccess(t, fx.platform, e5.ID, 3)

	hidden := idsOf(t, fx, true)
	if !sameIDs(hidden, []int64{e1.ID, e4.ID, e5.ID}) {
		t.Fatalf("hide = %v, want %v", hidden, []int64{e1.ID, e4.ID, e5.ID})
	}
	if all := idsOf(t, fx, false); len(all) != 5 {
		t.Fatalf("semua = %v", all)
	}
	if _, err := fx.platform.Exec(context.Background(), `UPDATE executions SET status = 'failed' WHERE id = $1`, e3.ID); err != nil {
		t.Fatal(err)
	}
	afterFail := idsOf(t, fx, true)
	if !containsID(afterFail, e2.ID) || !containsID(afterFail, e3.ID) {
		t.Fatalf("rollback gagal harus tetap tampil: %v", afterFail)
	}
}

func TestAccess_ViewCannotMutate(t *testing.T) {
	fx := newFixture(t, "view")
	rec := jsonAs(t, fx, http.MethodPost, "/api/databases/"+strconv.FormatInt(fx.dbID, 10)+"/migrations", fx.editor,
		map[string]any{"title": "x", "up_sql": "SELECT 1", "down_sql": "SELECT 1"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	rec = jsonAs(t, fx, http.MethodGet, "/api/databases/"+strconv.FormatInt(fx.dbID, 10)+"/migrations", fx.editor, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
}

type fixture struct {
	platform *pgxpool.Pool
	srv      *testutil.Server
	dbID     int64
	editor   authz.Principal
}

func newFixture(t *testing.T, level string) fixture {
	t.Helper()
	platform := testutil.NewPlatformDB(t)
	srv := testutil.NewServer(t, platform)
	userID := testutil.CreateUser(t, platform, "editor", false)
	user := authz.Principal{UserID: userID, Username: "editor"}
	groupID := testutil.CreateGroup(t, platform, "dev")
	testutil.AddMember(t, platform, groupID, userID)
	dbID := testutil.CreateConnection(t, platform, "target")
	testutil.Grant(t, platform, groupID, dbID, level)
	return fixture{platform: platform, srv: srv, dbID: dbID, editor: user}
}

func create(t *testing.T, fx fixture, title, up, down string, irrev bool, reason string) migration.Migration {
	t.Helper()
	m, err := fx.srv.Service.Create(context.Background(), fx.editor, fx.dbID, migration.Input{
		Title: title, UpSQL: up, DownSQL: down, Irreversible: irrev, IrreversibleReason: reason,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func submit(t *testing.T, fx fixture, id int64) migration.Execution {
	t.Helper()
	e, err := fx.srv.Service.Submit(context.Background(), fx.editor, fx.dbID, id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func forceSuccess(t *testing.T, db platformdb.DBTX, execID int64, version int32) {
	t.Helper()
	if version == 0 {
		if _, err := db.Exec(context.Background(), `UPDATE executions SET status = 'success', finished_at = now() WHERE id = $1`, execID); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := db.Exec(context.Background(), `
		UPDATE executions SET status = 'success', version_applied = $2, finished_at = now() WHERE id = $1`, execID, version); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `
		UPDATE migrations SET version = $2 WHERE id = (SELECT migration_id FROM executions WHERE id = $1)`, execID, version); err != nil {
		t.Fatal(err)
	}
}

func clearVersion(t *testing.T, db platformdb.DBTX, migrationID int64) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `UPDATE migrations SET version = NULL WHERE id = $1`, migrationID); err != nil {
		t.Fatal(err)
	}
}

func idsOf(t *testing.T, fx fixture, hide bool) []int64 {
	t.Helper()
	list, _, err := fx.srv.Service.ListExecutions(context.Background(), fx.dbID, hide, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	return ids
}

func sameIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsID(ids []int64, id int64) bool {
	for _, n := range ids {
		if n == id {
			return true
		}
	}
	return false
}

func isReason(err error, reason string) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Reason == reason
}

func jsonAs(t *testing.T, fx fixture, method, path string, p authz.Principal, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req = req.WithContext(authz.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	fx.srv.Handler.ServeHTTP(rec, req)
	return rec
}
