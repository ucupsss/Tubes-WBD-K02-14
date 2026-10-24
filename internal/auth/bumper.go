package auth

import (
	"context"
	"fmt"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// PgPrivilegeBumper menaikkan users.privilege_version. Session yang privilege_version-nya tertinggal
// dirotasi pada request berikutnya (D6).
type PgPrivilegeBumper struct {
	db platformdb.DBTX
}

func NewPrivilegeBumper(db platformdb.DBTX) *PgPrivilegeBumper {
	return &PgPrivilegeBumper{db: db}
}

func (b *PgPrivilegeBumper) BumpPrivilege(ctx context.Context, userIDs ...int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	const q = `
		UPDATE users SET privilege_version = privilege_version + 1, updated_at = now()
		WHERE id = ANY($1)`
	if _, err := b.db.Exec(ctx, q, userIDs); err != nil {
		return fmt.Errorf("auth.BumpPrivilege: %w", err)
	}
	return nil
}

func (b *PgPrivilegeBumper) BumpGroupMembers(ctx context.Context, groupID int64) error {
	const q = `
		UPDATE users SET privilege_version = privilege_version + 1, updated_at = now()
		WHERE id IN (SELECT user_id FROM group_members WHERE group_id = $1)`
	if _, err := b.db.Exec(ctx, q, groupID); err != nil {
		return fmt.Errorf("auth.BumpGroupMembers: %w", err)
	}
	return nil
}

var _ PrivilegeBumper = (*PgPrivilegeBumper)(nil)
