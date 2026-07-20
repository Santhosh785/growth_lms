package auth

// Role names, matching the memberships.role CHECK constraint in
// db/migrations/000002_auth_tenancy.up.sql.
const (
	RoleOwner     = "owner"
	RoleTeacher   = "teacher"
	RoleModerator = "moderator"
	RoleLearner   = "learner"
)

// permissionMatrix documents which actions each organization role is
// allowed to perform. It is enforced today via explicit RequireRole(...)
// calls on each route (see internal/httpserver/middleware), not by
// consulting this map at request time — it exists as a single, testable,
// living reference for the role model that Task 4/5/6 route permissions
// should extend rather than inventing ad hoc checks.
var permissionMatrix = map[string][]string{
	RoleOwner: {
		"org.update", "org.delete",
		"member.invite", "member.role.change", "member.remove",
		"apitoken.create", "apitoken.revoke",
	},
	RoleTeacher: {
		"member.invite",
	},
	RoleModerator: {
		"member.invite",
	},
	RoleLearner: {},
}

// Can reports whether the given role is documented as permitted to
// perform action.
func Can(role, action string) bool {
	for _, a := range permissionMatrix[role] {
		if a == action {
			return true
		}
	}
	return false
}
