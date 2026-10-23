package authz

import (
	"context"
	"fmt"
)

// Authorizer menghitung akses efektif dan memutuskan apakah Principal boleh menjalankan aksi.
type Authorizer interface {
	EffectiveLevel(ctx context.Context, userID, dbID int64) (Level, error) // max(levels), D1
	AccessibleDatabases(ctx context.Context, userID int64) ([]DatabaseAccess, error)
	Can(ctx context.Context, p Principal, a Action, dbID int64) (bool, error)
}

type grantAuthorizer struct {
	grants GrantSource
}

// NewAuthorizer membuat Authorizer yang membaca grant dari GrantSource (diimplementasi dbconn).
func NewAuthorizer(grants GrantSource) Authorizer {
	return &grantAuthorizer{grants: grants}
}

// EffectiveLevel = level tertinggi dari semua group user untuk database tsb (D1).
// User tanpa group/grant mendapat LevelNone.
func (a *grantAuthorizer) EffectiveLevel(ctx context.Context, userID, dbID int64) (Level, error) {
	levels, err := a.grants.LevelsForUser(ctx, userID, dbID)
	if err != nil {
		return LevelNone, fmt.Errorf("authz.EffectiveLevel: %w", err)
	}
	effective := LevelNone
	for _, l := range levels {
		if l > effective {
			effective = l
		}
	}
	return effective, nil
}

// AccessibleDatabases mengembalikan database yang bisa diakses user (sudah di-max per DB oleh GrantSource).
func (a *grantAuthorizer) AccessibleDatabases(ctx context.Context, userID int64) ([]DatabaseAccess, error) {
	dbs, err := a.grants.AccessibleDatabases(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("authz.AccessibleDatabases: %w", err)
	}
	return dbs, nil
}

// Can memeriksa aksi terhadap policy. dbID hanya dipakai untuk aksi db.*.
func (a *grantAuthorizer) Can(ctx context.Context, p Principal, act Action, dbID int64) (bool, error) {
	req, ok := RequirementFor(act)
	if !ok {
		return false, nil
	}
	if req.Admin && !p.IsAdmin {
		return false, nil
	}
	if !req.NeedsDatabase() {
		return true, nil
	}
	level, err := a.EffectiveLevel(ctx, p.UserID, dbID)
	if err != nil {
		return false, err
	}
	return level >= req.MinLevel, nil
}
