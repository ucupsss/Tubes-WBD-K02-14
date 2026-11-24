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
