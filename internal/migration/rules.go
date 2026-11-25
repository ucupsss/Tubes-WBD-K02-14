package migration

// head adalah cuplikan yang cukup untuk memutuskan submit, forward, dan rollback
// tanpa menyentuh database.
type head struct {
	Version       *int32
	Top           *int32
	Frozen        bool
	Irreversible  bool
	Open          bool
	HasFailed     bool
	HasRolledBack bool
	ActiveSuccess bool
	// PartialID adalah execution apply/forward terakhir yang berhenti partial dan belum di-revert
	// sukses. Batch yang sudah committed di dalamnya boleh dibatalkan lewat execution rollback.
	PartialID           *int64
	PartialRevisionID   int64
	PartialIrreversible bool
	// OpenOther: ada execution terbuka selain PartialID itu sendiri (mis. revert yang sedang diajukan).
	OpenOther bool
}

func (h head) canSubmit() error {
	if h.Open {
		return ErrBlocked
	}
	if h.Version != nil {
		return ErrAlreadyActive
	}
	if h.Frozen {
		return ErrNotSubmittable
	}
	return nil
}

func (h head) canForward() error {
	if h.Open {
		return ErrBlocked
	}
	if h.Version != nil {
		return ErrAlreadyActive
	}
	if !h.Frozen || (!h.HasFailed && !h.HasRolledBack) {
		return ErrNotForwardable
	}
	return nil
}

func (h head) canRollback() error {
	if h.PartialID != nil {
		// Revert batch committed dari execution partial (D36): bukan rollback rantai aktif.
		if h.OpenOther {
			return ErrBlocked
		}
		if h.PartialIrreversible {
			return ErrIrreversible
		}
		return nil
	}
	if h.Open {
		return ErrBlocked
	}
	if h.Version == nil || !h.ActiveSuccess {
		return ErrNotActive
	}
	if h.Top == nil || *h.Version != *h.Top {
		return ErrNotTop
	}
	if h.Irreversible {
		return ErrIrreversible
	}
	return nil
}

// hiddenByRollback melaporkan apakah satu baris riwayat disembunyikan saat Hide rollback aktif (D33).
// cancelledBySuccess berarti ada rollback berstatus success yang menunjuk ke execution ini.
func hiddenByRollback(kind, status string, cancelledBySuccess bool) bool {
	if cancelledBySuccess {
		return true
	}
	return kind == KindRollback && status == StatusSuccess
}
