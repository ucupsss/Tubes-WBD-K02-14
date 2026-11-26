package approval_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// fakeRepo: dua group (10 dipimpin user 1, 20 dipimpin user 2) dengan satu ronde.
type fakeRepo struct {
	rows      map[int64]approval.Decision // group → keputusan
	leaders   map[int64]int64             // group → leader
	openCount int
	maxRound  int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		rows:    map[int64]approval.Decision{10: approval.Pending, 20: approval.Pending},
		leaders: map[int64]int64{10: 1, 20: 2},
	}
}

func (r *fakeRepo) OpenRound(context.Context, platformdb.DBTX, int64, int64, int) (int, error) {
	return r.openCount, nil
}
func (r *fakeRepo) MaxRound(context.Context, platformdb.DBTX, int64) (int, error) {
	return r.maxRound, nil
}
func (r *fakeRepo) Decide(_ context.Context, _ platformdb.DBTX, _ int64, _ int, userID int64, d approval.Decision, _ string) (int, error) {
	n := 0
	for g, cur := range r.rows {
		if cur == approval.Pending && r.leaders[g] == userID {
			r.rows[g] = d
			n++
		}
	}
	return n, nil
}
func (r *fakeRepo) LeadsRow(_ context.Context, _ platformdb.DBTX, _ int64, _ int, userID int64) (bool, error) {
	for g := range r.rows {
		if r.leaders[g] == userID {
			return true, nil
		}
	}
	return false, nil
}
func (r *fakeRepo) Counts(context.Context, platformdb.DBTX, int64, int) (int, int, error) {
	var p, rej int
	for _, d := range r.rows {
		switch d {
		case approval.Pending:
			p++
		case approval.Rejected:
			rej++
		}
	}
	return p, rej, nil
}
func (r *fakeRepo) Missing(context.Context, platformdb.DBTX, int64, int64, int) ([]int64, error) {
	var m []int64
	for g, d := range r.rows {
		if d != approval.Approved {
			m = append(m, g)
		}
	}
	return m, nil
}
func (r *fakeRepo) GrantGroupCount(context.Context, platformdb.DBTX, int64) (int, error) {
	return len(r.rows), nil
}
func (r *fakeRepo) ListByExecution(context.Context, platformdb.DBTX, int64) ([]approval.Approval, error) {
	return nil, nil
}
func (r *fakeRepo) PendingForLeader(context.Context, platformdb.DBTX, int64) ([]approval.PendingItem, error) {
	return nil, nil
}

type fakeExecs struct {
	info   approval.ExecInfo
	status string
	round  int
}

func (e *fakeExecs) Get(context.Context, platformdb.DBTX, int64) (approval.ExecInfo, error) {
	return e.info, nil
}
func (e *fakeExecs) SetStatus(_ context.Context, _ platformdb.DBTX, _ int64, s string) error {
	e.status = s
	return nil
}
func (e *fakeExecs) SetRound(_ context.Context, _ platformdb.DBTX, _ int64, r int) error {
	e.round = r
	return nil
}

type directTx struct{}

func (directTx) InTx(_ context.Context, fn func(platformdb.DBTX) error) error { return fn(nil) }

func newSvc(repo *fakeRepo, ex *fakeExecs) *approval.Service {
	return approval.NewService(nil, directTx{}, repo, ex)
}

func principal(id int64) authz.Principal { return authz.Principal{UserID: id, Username: "u"} }

func TestDecide_AllLeadersApprove(t *testing.T) {
	repo, ex := newFakeRepo(), &fakeExecs{info: approval.ExecInfo{DatabaseID: 1, Round: 1, Status: approval.StatusPendingReview}}
	svc := newSvc(repo, ex)

	out, err := svc.Decide(context.Background(), principal(1), 7, true, "")
	if err != nil || out.Status != approval.StatusPendingReview {
		t.Fatalf("approval pertama: out=%v err=%v", out, err)
	}
	if ex.status != "" {
		t.Fatalf("status berubah terlalu dini: %q", ex.status)
	}
	out, err = svc.Decide(context.Background(), principal(2), 7, true, "")
	if err != nil || out.Status != approval.StatusApproved || ex.status != approval.StatusApproved {
		t.Fatalf("approval terakhir: out=%v status=%q err=%v", out, ex.status, err)
	}
}

func TestDecide_RejectEndsExecution(t *testing.T) {
	repo, ex := newFakeRepo(), &fakeExecs{info: approval.ExecInfo{Round: 1, Status: approval.StatusPendingReview}}
	out, err := newSvc(repo, ex).Decide(context.Background(), principal(1), 7, false, "tidak setuju")
	if err != nil || out.Status != approval.StatusRejected || ex.status != approval.StatusRejected {
		t.Fatalf("out=%v status=%q err=%v", out, ex.status, err)
	}
}

func TestDecide_NonLeaderForbidden(t *testing.T) {
	repo, ex := newFakeRepo(), &fakeExecs{info: approval.ExecInfo{Round: 1, Status: approval.StatusPendingReview}}
	_, err := newSvc(repo, ex).Decide(context.Background(), principal(99), 7, true, "")
	if !errors.Is(err, approval.ErrNotLeader) || apperr.KindOf(err) != apperr.KindForbidden {
		t.Fatalf("err = %v, want ErrNotLeader (403)", err)
	}
}

func TestDecide_DoubleDecisionConflict(t *testing.T) {
	repo, ex := newFakeRepo(), &fakeExecs{info: approval.ExecInfo{Round: 1, Status: approval.StatusPendingReview}}
	svc := newSvc(repo, ex)
	if _, err := svc.Decide(context.Background(), principal(1), 7, true, ""); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Decide(context.Background(), principal(1), 7, true, "")
	if !errors.Is(err, approval.ErrAlreadyDecided) {
		t.Fatalf("err = %v, want ErrAlreadyDecided", err)
	}
}

func TestDecide_NotPendingReview(t *testing.T) {
	for _, status := range []string{"approved", "queued", "running", "success", "failed", "rejected"} {
		repo, ex := newFakeRepo(), &fakeExecs{info: approval.ExecInfo{Round: 1, Status: status}}
		_, err := newSvc(repo, ex).Decide(context.Background(), principal(1), 7, true, "")
		if !errors.Is(err, approval.ErrNotPending) {
			t.Fatalf("status %q: err = %v, want ErrNotPending", status, err)
		}
	}
}

func TestDecide_OneLeaderTwoGroupsCoversBoth(t *testing.T) {
	repo := newFakeRepo()
	repo.leaders[20] = 1 // user 1 memimpin kedua group
	ex := &fakeExecs{info: approval.ExecInfo{Round: 1, Status: approval.StatusPendingReview}}
	out, err := newSvc(repo, ex).Decide(context.Background(), principal(1), 7, true, "")
	if err != nil || out.Status != approval.StatusApproved {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestOpenRound_NoGroupsRejected(t *testing.T) {
	repo := newFakeRepo()
	repo.openCount = 0
	err := newSvc(repo, &fakeExecs{}).OpenRound(context.Background(), nil, 7, 1, 1)
	if !errors.Is(err, approval.ErrNoGroups) {
		t.Fatalf("err = %v, want ErrNoGroups", err)
	}
}

func TestRevoke_OpensNextRoundAndReturnsToPendingReview(t *testing.T) {
	repo, ex := newFakeRepo(), &fakeExecs{}
	repo.openCount, repo.maxRound = 2, 1
	round, err := newSvc(repo, ex).Revoke(context.Background(), nil, 7, 1)
	if err != nil || round != 2 || ex.round != 2 || ex.status != approval.StatusPendingReview {
		t.Fatalf("round=%d exRound=%d status=%q err=%v", round, ex.round, ex.status, err)
	}
}

func TestVerify(t *testing.T) {
	repo := newFakeRepo()
	svc := newSvc(repo, &fakeExecs{})
	v, err := svc.Verify(context.Background(), nil, 7, 1, 1)
	if err != nil || v.Complete || len(v.MissingGroupIDs) != 2 {
		t.Fatalf("pending: %+v err=%v", v, err)
	}
	repo.rows[10], repo.rows[20] = approval.Approved, approval.Approved
	v, _ = svc.Verify(context.Background(), nil, 7, 1, 1)
	if !v.Complete {
		t.Fatalf("semua approved tetapi tidak complete: %+v", v)
	}
	repo.rows[20] = approval.Rejected
	v, _ = svc.Verify(context.Background(), nil, 7, 1, 1)
	if v.Complete {
		t.Fatalf("ada rejected seharusnya tidak complete: %+v", v)
	}
}

func TestUnavailableExecutions(t *testing.T) {
	svc := approval.NewService(nil, directTx{}, newFakeRepo(), nil)
	_, err := svc.Decide(context.Background(), principal(1), 7, true, "")
	if !errors.Is(err, approval.ErrExecUnavailable) {
		t.Fatalf("err = %v, want ErrExecUnavailable", err)
	}
}
