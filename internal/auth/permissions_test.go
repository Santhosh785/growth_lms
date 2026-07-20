package auth

import "testing"

func TestCan_CourseDomainActions(t *testing.T) {
	granted := []string{
		"course.create", "course.publish", "chapter.create", "lesson.create",
		"block.create", "media.upload", "collection.manage", "tag.manage",
	}
	ownerOnly := []string{"category.create", "category.update", "category.delete"}

	for _, action := range granted {
		if !Can(RoleOwner, action) {
			t.Errorf("expected owner to be granted %q", action)
		}
		if !Can(RoleTeacher, action) {
			t.Errorf("expected teacher to be granted %q", action)
		}
		if Can(RoleModerator, action) {
			t.Errorf("expected moderator NOT to be granted %q", action)
		}
		if Can(RoleLearner, action) {
			t.Errorf("expected learner NOT to be granted %q", action)
		}
	}

	for _, action := range ownerOnly {
		if !Can(RoleOwner, action) {
			t.Errorf("expected owner to be granted %q", action)
		}
		if Can(RoleTeacher, action) {
			t.Errorf("expected teacher NOT to be granted owner-only %q", action)
		}
		if Can(RoleModerator, action) {
			t.Errorf("expected moderator NOT to be granted %q", action)
		}
		if Can(RoleLearner, action) {
			t.Errorf("expected learner NOT to be granted %q", action)
		}
	}
}

func TestCan_UnknownAction(t *testing.T) {
	if Can(RoleOwner, "no.such.action") {
		t.Error("expected unknown action to be denied")
	}
}
