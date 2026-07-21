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

// commerceDomainActions are the Task 6 commerce authoring actions granted to
// both RoleOwner and RoleTeacher — mirrors courseDomainActions' owner/teacher
// split above.
var commerceDomainActions = []string{
	"offer.create", "offer.update", "offer.archive",
	"discount.create", "discount.update", "discount.archive",
	"invitetoken.create", "entitlement.grant",
}

// ownerOnlyCommerceDomainActions are commerce actions restricted to
// RoleOwner: refunds and org-level financial visibility are owner-only,
// unlike the creator-facing offer/discount management above.
//
// Note: platform-commission-config and the platform-owner cross-org
// dashboard are NOT represented here — those are enforced via
// RequirePlatformOwner (profiles.is_platform_owner), not this org-role
// matrix, since they are not scoped to any single organization's roles.
var ownerOnlyCommerceDomainActions = []string{
	"refund.initiate", "dashboard.org.view", "report.revenue.view",
}

var permissionMatrix = map[string][]string{
	RoleOwner: append(append(append(append([]string{
		"org.update", "org.delete",
		"member.invite", "member.role.change", "member.remove",
		"apitoken.create", "apitoken.revoke",
	}, courseDomainActions...), ownerOnlyCourseDomainActions...),
		commerceDomainActions...), ownerOnlyCommerceDomainActions...),
	RoleTeacher: append(append([]string{
		"member.invite",
	}, courseDomainActions...), commerceDomainActions...),
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
