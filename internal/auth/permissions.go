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
// courseDomainActions are the Task 4 authoring actions granted to both
// RoleOwner and RoleTeacher — moderator remains learner-equivalent for
// authoring per Task 3's Q54 decision, and learners never author.
var courseDomainActions = []string{
	"course.create", "course.update", "course.delete", "course.publish",
	"course.unpublish", "course.archive", "course.duplicate",
	"chapter.create", "chapter.update", "chapter.delete",
	"lesson.create", "lesson.update", "lesson.delete",
	"block.create", "block.update", "block.delete",
	"media.upload", "collection.manage", "tag.manage",
}

// ownerOnlyCourseDomainActions are curated-taxonomy actions restricted to
// RoleOwner: categories are a small, deliberate set an org owner manages,
// unlike tags' freeform get-or-create.
var ownerOnlyCourseDomainActions = []string{
	"category.create", "category.update", "category.delete",
}

var permissionMatrix = map[string][]string{
	RoleOwner: append(append([]string{
		"org.update", "org.delete",
		"member.invite", "member.role.change", "member.remove",
		"apitoken.create", "apitoken.revoke",
	}, courseDomainActions...), ownerOnlyCourseDomainActions...),
	RoleTeacher: append([]string{
		"member.invite",
	}, courseDomainActions...),
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
