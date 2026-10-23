package authz

// Requirement adalah syarat yang harus dipenuhi Principal untuk menjalankan satu Action.
type Requirement struct {
	Admin    bool  // butuh Platform Admin (users.is_admin)
	MinLevel Level // level akses minimal ke database; LevelNone = aksi tidak terkait database
}

// NeedsDatabase bernilai true untuk aksi db.* yang dicek terhadap {dbID} di URL.
func (r Requirement) NeedsDatabase() bool { return r.MinLevel > LevelNone }

// policy adalah SATU-SATUNYA tempat aturan aksi. Aksi yang tidak terdaftar selalu ditolak.
//
// Platform Admin bukan tingkat akses database (D2): aksi db.* hanya melihat grant group,
// tidak melihat flag is_admin.
var policy = map[Action]Requirement{
	ActAdminManageUsers:       {Admin: true},
	ActAdminManageGroups:      {Admin: true},
	ActAdminManageConnections: {Admin: true},
	ActAdminManageGrants:      {Admin: true},

	ActDBView:           {MinLevel: LevelView},
	ActMigrationView:    {MinLevel: LevelView},
	ActMigrationCreate:  {MinLevel: LevelEdit},
	ActMigrationEdit:    {MinLevel: LevelEdit},
	ActMigrationExecute: {MinLevel: LevelEdit},
	ActMigrationDelete:  {MinLevel: LevelEdit},
	ActMigrationExplain: {MinLevel: LevelEdit}, // EXPLAIN ANALYZE benar-benar mengeksekusi statement
}

// RequirementFor mengembalikan syarat sebuah aksi. ok=false berarti aksi tidak dikenal (deny).
func RequirementFor(a Action) (req Requirement, ok bool) {
	req, ok = policy[a]
	return req, ok
}
