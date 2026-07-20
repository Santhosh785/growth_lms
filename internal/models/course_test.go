package models_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/models"
	"growth-lms/internal/testutil"
)

func TestCourse_StatusTransitions(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgID := seedOrgWithOwner(t, admin, ownerID, "course-status-"+uuid.NewString())

	tx, err := dbctx.Begin(ctx, pool, ownerID, orgID, "owner")
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	courses := models.NewCourseRepo()
	course, err := courses.Create(ctx, tx.Tx, orgID, ownerID, "Intro to Testing", "desc", nil)
	require.NoError(t, err)
	require.Equal(t, models.CourseStatusDraft, course.Status)

	// draft -> review
	course, err = courses.SetStatus(ctx, tx.Tx, course.ID, models.CourseStatusReview)
	require.NoError(t, err)
	require.Equal(t, models.CourseStatusReview, course.Status)

	// review -> published (publish always snapshots)
	course, err = courses.Publish(ctx, tx.Tx, course.ID)
	require.NoError(t, err)
	require.Equal(t, models.CourseStatusPublished, course.Status)
	require.NotNil(t, course.PublishedAt)

	versions := models.NewCourseVersionRepo()
	_, err = versions.Snapshot(ctx, tx.Tx, course.ID, orgID, ownerID)
	require.NoError(t, err)

	list, err := versions.List(ctx, tx.Tx, course.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// published -> unpublished
	course, err = courses.SetStatus(ctx, tx.Tx, course.ID, models.CourseStatusUnpublished)
	require.NoError(t, err)
	require.Equal(t, models.CourseStatusUnpublished, course.Status)

	// unpublished -> archived
	course, err = courses.Archive(ctx, tx.Tx, course.ID)
	require.NoError(t, err)
	require.Equal(t, models.CourseStatusArchived, course.Status)
	require.NotNil(t, course.ArchivedAt)
}

func TestCourse_Duplicate(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgID := seedOrgWithOwner(t, admin, ownerID, "course-dup-"+uuid.NewString())

	tx, err := dbctx.Begin(ctx, pool, ownerID, orgID, "owner")
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	courses := models.NewCourseRepo()
	chapters := models.NewChapterRepo()
	lessons := models.NewLessonRepo()
	blocks := models.NewBlockRepo()

	source, err := courses.Create(ctx, tx.Tx, orgID, ownerID, "Source Course", "desc", nil)
	require.NoError(t, err)

	ch, err := chapters.Create(ctx, tx.Tx, source.ID, orgID, ownerID, "Chapter 1", 1.0)
	require.NoError(t, err)
	lsn, err := lessons.Create(ctx, tx.Tx, ch.ID, source.ID, orgID, ownerID, "Lesson 1", 1.0)
	require.NoError(t, err)

	content, _ := json.Marshal(models.ImageBlockContent{AssetID: "asset-123", AltText: "alt"})
	_, err = blocks.Create(ctx, tx.Tx, lsn.ID, source.ID, orgID, ownerID, models.BlockTypeImage, content, 1.0)
	require.NoError(t, err)

	dup, err := courses.Duplicate(ctx, tx.Tx, source.ID, ownerID)
	require.NoError(t, err)
	require.NotEqual(t, source.ID, dup.ID, "duplicate must have a new course ID")
	require.Equal(t, models.CourseStatusDraft, dup.Status, "duplicate must start in draft status")

	dupChapters, err := chapters.ListByCourse(ctx, tx.Tx, dup.ID)
	require.NoError(t, err)
	require.Len(t, dupChapters, 1)
	require.NotEqual(t, ch.ID, dupChapters[0].ID, "duplicate chapter must have a new ID")

	dupLessons, err := lessons.ListByChapter(ctx, tx.Tx, dupChapters[0].ID)
	require.NoError(t, err)
	require.Len(t, dupLessons, 1)
	require.NotEqual(t, lsn.ID, dupLessons[0].ID, "duplicate lesson must have a new ID")

	dupBlocks, err := blocks.ListByLesson(ctx, tx.Tx, dupLessons[0].ID)
	require.NoError(t, err)
	require.Len(t, dupBlocks, 1)

	var dupContent models.ImageBlockContent
	require.NoError(t, json.Unmarshal(dupBlocks[0].Content, &dupContent))
	require.Equal(t, "asset-123", dupContent.AssetID, "duplicate block must reference the SAME asset_id, not a new copy")
}

func TestCourseVersion_Restore(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgID := seedOrgWithOwner(t, admin, ownerID, "course-restore-"+uuid.NewString())

	tx, err := dbctx.Begin(ctx, pool, ownerID, orgID, "owner")
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	courses := models.NewCourseRepo()
	chapters := models.NewChapterRepo()
	versions := models.NewCourseVersionRepo()

	course, err := courses.Create(ctx, tx.Tx, orgID, ownerID, "Restorable Course", "v1 desc", nil)
	require.NoError(t, err)
	_, err = chapters.Create(ctx, tx.Tx, course.ID, orgID, ownerID, "Chapter A", 1.0)
	require.NoError(t, err)

	v1, err := versions.Snapshot(ctx, tx.Tx, course.ID, orgID, ownerID)
	require.NoError(t, err)
	require.Equal(t, 1, v1.VersionNumber)

	// Mutate: add a second chapter, changing live state away from v1.
	_, err = chapters.Create(ctx, tx.Tx, course.ID, orgID, ownerID, "Chapter B", 2.0)
	require.NoError(t, err)
	liveChapters, err := chapters.ListByCourse(ctx, tx.Tx, course.ID)
	require.NoError(t, err)
	require.Len(t, liveChapters, 2)

	restored, err := versions.Restore(ctx, tx.Tx, course.ID, v1.ID, ownerID)
	require.NoError(t, err)
	require.Equal(t, 2, restored.VersionNumber, "restore must create a NEW version, not overwrite v1")

	all, err := versions.List(ctx, tx.Tx, course.ID)
	require.NoError(t, err)
	require.Len(t, all, 2, "v1 must still exist after restore")

	postRestoreChapters, err := chapters.ListByCourse(ctx, tx.Tx, course.ID)
	require.NoError(t, err)
	require.Len(t, postRestoreChapters, 1, "restore must reset live chapters back to v1's single chapter")
	require.Equal(t, "Chapter A", postRestoreChapters[0].Title)
}

func TestChapter_DeleteBlockedByLessons(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerID := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgID := seedOrgWithOwner(t, admin, ownerID, "chapter-del-"+uuid.NewString())

	tx, err := dbctx.Begin(ctx, pool, ownerID, orgID, "owner")
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	courses := models.NewCourseRepo()
	chapters := models.NewChapterRepo()
	lessons := models.NewLessonRepo()

	course, err := courses.Create(ctx, tx.Tx, orgID, ownerID, "Course", "", nil)
	require.NoError(t, err)
	ch, err := chapters.Create(ctx, tx.Tx, course.ID, orgID, ownerID, "Chapter", 1.0)
	require.NoError(t, err)
	_, err = lessons.Create(ctx, tx.Tx, ch.ID, course.ID, orgID, ownerID, "Lesson", 1.0)
	require.NoError(t, err)

	err = chapters.Delete(ctx, tx.Tx, ch.ID)
	require.Error(t, err)
	var hasChildren models.ErrHasChildren
	require.ErrorAs(t, err, &hasChildren)
	require.Equal(t, 1, hasChildren.Count)
}
