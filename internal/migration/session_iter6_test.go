package migration

import (
	"context"
	"sync"
	"testing"
	"time"
)

type flakyLease struct {
	mu    sync.Mutex
	fails int
	beats int
}

func (f *flakyLease) Heartbeat(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return errHeartbeat
	}
	f.beats++
	return nil
}

func (f *flakyLease) Release(context.Context) error { return nil }
func (f *flakyLease) DatabaseID() int64             { return 1 }
func (f *flakyLease) ExecutionID() int64            { return 1 }

var errHeartbeat = errString("putus sebentar")

type errString string

func (e errString) Error() string { return string(e) }

func TestIter6_HeartbeatKeepsTryingAfterABlip(t *testing.T) {
	prev := heartbeatEvery
	heartbeatEvery = 20 * time.Millisecond
	t.Cleanup(func() { heartbeatEvery = prev })
	lease := &flakyLease{fails: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var touchMu sync.Mutex
	touches := 0
	go heartbeat(ctx, lease, func(context.Context) error {
		touchMu.Lock()
		touches++
		touchMu.Unlock()
		return nil
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lease.mu.Lock()
		beats := lease.beats
		lease.mu.Unlock()
		touchMu.Lock()
		touched := touches
		touchMu.Unlock()
		// claimed_at job tetap disegarkan, termasuk pada tick saat perpanjangan lease gagal.
		if beats >= 1 && touched >= 2 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("heartbeat berhenti setelah satu kegagalan koneksi ke platform")
}
