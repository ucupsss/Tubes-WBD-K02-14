//go:build integration

package approval_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

// memExecs menggantikan tabel executions milik C: tabel execution_approvals tidak punya FK ke sana.
type memExecs struct {
	info   approval.ExecInfo
	status string
}

func (m *memExecs) Get(context.Context, platformdb.DBTX, int64) (approval.ExecInfo, error) {
	i := m.info
	if m.status != "" {
		i.Status = m.status
	}
	return i, nil
}
func (m *memExecs) SetStatus(_ context.Context, _ platformdb.DBTX, _ int64, s string) error {
	m.status = s
	return nil
}
func (m *memExecs) SetRound(_ context.Context, _ platformdb.DBTX, _ int64, r int) error {
	m.info.Round = r
	return nil
}

type env struct {
	pool            *pgxpool.Pool
	svc             *approval.Service
	execs           *memExecs
	dbID            int64
	devGroup        int64
	analystGroup    int64
	alice, bob, eve int64
}

const execID = int64(501)

// setup: dev (leader alice) dan analyst (leader bob) sama-sama punya akses ke satu database.
func setup(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	name, _ := testutil.NewTargetDB(t, "")
	e := &env{pool: pool, dbID: testutil.CreateConnection(t, pool, name)}
	e.alice = testutil.CreateUser(t, pool, "alice", false)
	e.bob = testutil.CreateUser(t, pool, "bob", false)
	e.eve = testutil.CreateUser(t, pool, "eve", false)
	e.devGroup = testutil.CreateGroup(t, pool, "dev")
	e.analystGroup = testutil.CreateGroup(t, pool, "analyst")
	testutil.AddMember(t, pool, e.devGroup, e.alice)
	testutil.AddMember(t, pool, e.analystGroup, e.bob)
	testutil.Grant(t, pool, e.devGroup, e.dbID, "edit")
	testutil.Grant(t, pool, e.analystGroup, e.dbID, "view")
	e.setLeader(t, e.devGroup, &e.alice)
	e.setLeader(t, e.analystGroup, &e.bob)

	e.execs = &memExecs{info: approval.ExecInfo{DatabaseID: e.dbID, Round: 1, Status: approval.StatusPendingReview}}
	e.svc = approval.NewService(pool, approval.PoolTx{Pool: pool}, approval.NewPgRepo(), e.execs)
	return e
}

func (e *env) setLeader(t *testing.T, groupID int64, userID *int64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE groups SET leader_id = $2 WHERE id = $1`, groupID, userID); err != nil {
		t.Fatal(err)
	}
}

func (e *env) open(t *testing.T, round int) {
	t.Helper()
	if err := e.svc.OpenRound(context.Background(), e.pool, execID, e.dbID, round); err != nil {
		t.Fatalf("OpenRound: %v", err)
	}
}

func (e *env) decide(t *testing.T, userID int64, ok bool) (approval.Outcome, error) {
	t.Helper()
	return e.svc.Decide(context.Background(), principal(userID), execID, ok, "")
}

func TestApproval_AllLeadersMustApprove(t *testing.T) {
	e := setup(t)
	e.open(t, 1)

	items, _ := e.svc.ListByExecution(context.Background(), execID)
	if len(items) != 2 {
		t.Fatalf("baris approval = %d, want 2 (satu per group dengan akses view/edit)", len(items))
	}
	out, err := e.decide(t, e.alice, true)
	if err != nil || out.Status != approval.StatusPendingReview {
		t.Fatalf("alice: out=%v err=%v", out, err)
	}
	out, err = e.decide(t, e.bob, true)
	if err != nil || out.Status != approval.StatusApproved || e.execs.status != approval.StatusApproved {
		t.Fatalf("bob: out=%v status=%q err=%v", out, e.execs.status, err)
	}
	v, err := e.svc.Verify(context.Background(), e.pool, execID, e.dbID, 1)
	if err != nil || !v.Complete {
		t.Fatalf("Verify: %+v err=%v", v, err)
	}
}

func TestApproval_NonLeaderAndDoubleDecision(t *testing.T) {
	e := setup(t)
	e.open(t, 1)
	if _, err := e.decide(t, e.eve, true); !errors.Is(err, approval.ErrNotLeader) {
		t.Fatalf("non-leader: err = %v", err)
	}
	if _, err := e.decide(t, e.alice, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.decide(t, e.alice, true); !errors.Is(err, approval.ErrAlreadyDecided) {
		t.Fatalf("keputusan ganda: err = %v", err)
	}
}

func TestApproval_RejectEndsExecution(t *testing.T) {
	e := setup(t)
	e.open(t, 1)
	out, err := e.decide(t, e.bob, false)
	if err != nil || out.Status != approval.StatusRejected || e.execs.status != approval.StatusRejected {
		t.Fatalf("out=%v status=%q err=%v", out, e.execs.status, err)
	}
	v, _ := e.svc.Verify(context.Background(), e.pool, execID, e.dbID, 1)
	if v.Complete {
		t.Fatal("execution rejected tidak boleh dianggap complete")
	}
}

func TestApproval_GroupWithoutLeaderStaysPending(t *testing.T) {
	e := setup(t)
	e.setLeader(t, e.analystGroup, nil)
	e.open(t, 1) // dibuka tetap berhasil: pengajuan tidak ditolak

	out, err := e.decide(t, e.alice, true)
	if err != nil || out.Status != approval.StatusPendingReview {
		t.Fatalf("dev approve: out=%v err=%v", out, err)
	}
	if e.execs.status == approval.StatusApproved {
		t.Fatal("tidak boleh approved selama group analyst belum punya leader")
	}
	// Leader ditetapkan belakangan → bisa memutuskan.
	e.setLeader(t, e.analystGroup, &e.bob)
	out, err = e.decide(t, e.bob, true)
	if err != nil || out.Status != approval.StatusApproved {
		t.Fatalf("setelah leader ditetapkan: out=%v err=%v", out, err)
	}
}

func TestApproval_OneLeaderTwoGroups(t *testing.T) {
	e := setup(t)
	e.setLeader(t, e.analystGroup, &e.alice) // alice memimpin dua group
	e.open(t, 1)
	out, err := e.decide(t, e.alice, true)
	if err != nil || out.Status != approval.StatusApproved {
		t.Fatalf("satu approval seharusnya menutup kedua group: out=%v err=%v", out, err)
	}
}

func TestApproval_SelfApprovalAllowed(t *testing.T) {
	// Requester tidak dicatat di approval, jadi leader yang mengajukan sendiri tetap bisa approve (D28).
	e := setup(t)
	e.open(t, 1)
	if _, err := e.decide(t, e.alice, true); err != nil {
		t.Fatalf("self-approval: %v", err)
	}
}

func TestApproval_LeaderChangeInvalidatesApproval(t *testing.T) {
	e := setup(t)
	e.open(t, 1)
	_, _ = e.decide(t, e.alice, true)
	_, _ = e.decide(t, e.bob, true)

	e.setLeader(t, e.analystGroup, &e.eve) // leader berganti setelah approval
	v, err := e.svc.Verify(context.Background(), e.pool, execID, e.dbID, 1)
	if err != nil || v.Complete || len(v.MissingGroupIDs) != 1 || v.MissingGroupIDs[0] != e.analystGroup {
		t.Fatalf("Verify: %+v err=%v", v, err)
	}
}

func TestApproval_NewGroupWithAccessInvalidatesAndRevokeOpensRound2(t *testing.T) {
	e := setup(t)
	e.open(t, 1)
	_, _ = e.decide(t, e.alice, true)
	_, _ = e.decide(t, e.bob, true)

	ops := testutil.CreateGroup(t, e.pool, "ops")
	testutil.Grant(t, e.pool, ops, e.dbID, "view")

	v, _ := e.svc.Verify(context.Background(), e.pool, execID, e.dbID, 1)
	if v.Complete || len(v.MissingGroupIDs) != 1 || v.MissingGroupIDs[0] != ops {
		t.Fatalf("group baru seharusnya membuat approval tidak lengkap: %+v", v)
	}

	round, err := e.svc.Revoke(context.Background(), e.pool, execID, e.dbID)
	if err != nil || round != 2 {
		t.Fatalf("Revoke: round=%d err=%v", round, err)
	}
	if e.execs.status != approval.StatusPendingReview || e.execs.info.Round != 2 {
		t.Fatalf("execution = %+v status=%q", e.execs.info, e.execs.status)
	}
	e.execs.status = "" // kembali ke info.Status = pending_review

	items, _ := e.svc.ListByExecution(context.Background(), execID)
	if len(items) != 2+3 {
		t.Fatalf("baris = %d, want 5 (ronde 1: 2, ronde 2: 3 group)", len(items))
	}
	v2, _ := e.svc.Verify(context.Background(), e.pool, execID, e.dbID, 2)
	if v2.Complete || len(v2.MissingGroupIDs) != 3 {
		t.Fatalf("ronde baru harus mulai dari nol: %+v", v2)
	}
}

func TestApproval_PendingInboxShowsOnlyLatestRound(t *testing.T) {
	e := setup(t)
	e.open(t, 1)
	items, err := e.svc.Pending(context.Background(), e.bob)
	if err != nil || len(items) != 1 || items[0].ExecutionID != execID || items[0].GroupID != e.analystGroup {
		t.Fatalf("inbox bob: %+v err=%v", items, err)
	}
	if items, _ := e.svc.Pending(context.Background(), e.eve); len(items) != 0 {
		t.Fatalf("eve bukan leader, inbox harus kosong: %+v", items)
	}

	e.open(t, 2) // ronde baru: baris ronde 1 yang masih pending tidak boleh muncul ganda
	items, _ = e.svc.Pending(context.Background(), e.bob)
	if len(items) != 1 || items[0].Round != 2 {
		t.Fatalf("inbox setelah ronde 2: %+v", items)
	}
}

func TestApproval_OpenRoundWithoutAccessFails(t *testing.T) {
	e := setup(t)
	name, _ := testutil.NewTargetDB(t, "")
	lonely := testutil.CreateConnection(t, e.pool, name)
	err := e.svc.OpenRound(context.Background(), e.pool, execID+1, lonely, 1)
	if !errors.Is(err, approval.ErrNoGroups) {
		t.Fatalf("err = %v, want ErrNoGroups", err)
	}
}
