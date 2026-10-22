package auth

import "context"

// PrivilegeBumper digunakan saat privilege user atau group naik (mis. dijadikan admin,
// masuk group, atau grant group naik dari view ke edit).
type PrivilegeBumper interface {
	BumpPrivilege(ctx context.Context, userIDs ...int64) error
	BumpGroupMembers(ctx context.Context, groupID int64) error
}
