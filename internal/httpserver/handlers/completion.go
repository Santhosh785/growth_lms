package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

const certificatePDFContentType = "application/pdf"

// evaluateAndIssueCertificateIfComplete is the single hook every
// lesson-completion call site (learner.go's ReportLessonProgress/
// CompleteLesson, quiz.go's SubmitQuiz on pass, assignment.go's
// SubmitAssignment) calls after marking a lesson complete, to check
// whether the learner has now satisfied courseID's completion rules and,
// if so, issue a certificate.
//
// There is deliberately no generic event bus here — a direct function
// call after each MarkComplete call site, matching this codebase's
// directness (see main-plan.md Stage 6).
//
// Idempotent via GetByLearnerAndCourse: re-evaluating an already-issued
// learner (e.g. retaking a quiz they'd already passed) is a no-op, not a
// duplicate-issuance error — UNIQUE (learner_id, course_id) on
// learner_certificate would reject a second Create anyway, but checking
// first avoids generating and uploading a PDF nobody will store.
//
// Certificate issuance is never learner-triggerable directly: there is no
// POST /certificates endpoint anywhere in this codebase. This function is
// the only code path that creates a learner_certificate row, and it only
// ever runs as a side effect of a completion event inside that learner's
// own authenticated request (never from a request one learner makes about
// another learner's progress).
func evaluateAndIssueCertificateIfComplete(ctx context.Context, tx models.Querier, d *AuthDeps, courseID, learnerID string) error {
	if _, err := d.Certificates.GetByLearnerAndCourse(ctx, tx, learnerID, courseID); err == nil {
		return nil
	} else if !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("handlers: check existing certificate: %w", err)
	}

	complete, err := models.EvaluateCompletion(ctx, tx, courseID, learnerID)
	if err != nil {
		return fmt.Errorf("handlers: evaluate course completion: %w", err)
	}
	if !complete {
		return nil
	}

	course, err := d.Courses.Get(ctx, tx, courseID)
	if err != nil {
		return fmt.Errorf("handlers: load course for certificate: %w", err)
	}
	learner, err := d.Profiles.GetByID(ctx, tx, learnerID)
	if err != nil {
		return fmt.Errorf("handlers: load learner for certificate: %w", err)
	}

	learnerName := learner.Email
	if learner.FullName != nil && *learner.FullName != "" {
		learnerName = *learner.FullName
	}

	certificateID := uuid.NewString()
	issuedAt := time.Now().UTC()

	pdfBytes, err := generateCertificatePDF(learnerName, course.Title, issuedAt, certificateID)
	if err != nil {
		return fmt.Errorf("handlers: render certificate pdf: %w", err)
	}

	storagePath := fmt.Sprintf("org/%s/courses/%s/certificates/%s.pdf", course.OrgID, course.ID, certificateID)
	if err := d.Storage.UploadServerSide(ctx, d.Config.Supabase.StorageBucket, storagePath, pdfBytes, certificatePDFContentType); err != nil {
		return fmt.Errorf("handlers: upload certificate pdf: %w", err)
	}

	if _, err := d.Certificates.Create(ctx, tx, course.OrgID, learnerID, courseID, certificateID, storagePath); err != nil {
		return fmt.Errorf("handlers: create certificate row: %w", err)
	}

	// TODO(Stage 7): enqueue a certificate-issued notification email to
	// the learner here once the async notification worker exists.

	return nil
}

// generateCertificatePDF renders a simple, single-page landscape
// certificate: title, learner name, course title, completion date, and
// certificate ID — no logo/signature/template beyond this (see
// grilling-record.md Q6: pure-Go github.com/go-pdf/fpdf, no headless
// browser, programmatic layout rather than HTML/CSS-templated).
func generateCertificatePDF(learnerName, courseTitle string, issuedAt time.Time, certificateID string) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 28)
	pdf.Ln(30)
	pdf.CellFormat(0, 16, "Certificate of Completion", "", 1, "C", false, 0, "")

	pdf.Ln(14)
	pdf.SetFont("Helvetica", "", 16)
	pdf.CellFormat(0, 10, "This certifies that", "", 1, "C", false, 0, "")

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "B", 22)
	pdf.CellFormat(0, 14, learnerName, "", 1, "C", false, 0, "")

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "", 16)
	pdf.CellFormat(0, 10, "has successfully completed the course", "", 1, "C", false, 0, "")

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 14, courseTitle, "", 1, "C", false, 0, "")

	pdf.Ln(16)
	pdf.SetFont("Helvetica", "", 12)
	pdf.CellFormat(0, 8, "Completion date: "+issuedAt.Format("January 2, 2006"), "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, "Certificate ID: "+certificateID, "", 1, "C", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GetCourseCertificate returns the caller's own certificate for this
// course, if issued, with a short-lived signed download URL for the PDF
// (reusing Task 4's CreateSignedURL pattern). 404 if the learner hasn't
// completed the course yet — there is no way to request early issuance.
// Gated by RequireEntitlement.
func GetCourseCertificate(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		cert, err := d.Certificates.GetByLearnerAndCourse(ctx, tx, ac.UserID, course.ID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "certificate not yet issued"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		downloadURL, err := d.Storage.CreateSignedURL(ctx, d.Config.Supabase.StorageBucket, cert.PDFStoragePath, time.Hour)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create download url"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"certificate_id": cert.CertificateID,
			"course_id":      cert.CourseID,
			"issued_at":      cert.IssuedAt,
			"download_url":   downloadURL,
		})
	}
}

// ListCertificates returns every certificate the caller has earned, for
// their dashboard. Requires only authentication (RequireEntitlement is
// course-scoped and doesn't apply here — this endpoint isn't about any
// one course).
func ListCertificates(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		certs, err := d.Certificates.ListByLearner(ctx, tx, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]gin.H, len(certs))
		for i, cert := range certs {
			out[i] = gin.H{
				"certificate_id": cert.CertificateID,
				"course_id":      cert.CourseID,
				"issued_at":      cert.IssuedAt,
			}
		}
		c.JSON(http.StatusOK, gin.H{"certificates": out})
	}
}

// VerifyCertificate is PUBLIC and unauthenticated (mounted directly on
// the gin.Engine, no Authenticate/WithRequestTx middleware at all — see
// server.go). It has no user/org session context to scope an RLS query
// with, so it queries the verify_certificate() SECURITY DEFINER function
// (db/migrations/000005_certificate_verification.up.sql) directly against
// the raw pool rather than going through dbctx.Begin/RequestTx: that
// function's own RETURNS TABLE signature is what limits the response to
// learner_name/course_title/issued_at, nothing else about the
// certificate or its owner ever leaks.
func VerifyCertificate(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		certificateID := c.Param("certificateId")
		ctx := c.Request.Context()

		// learner_name comes from profiles.full_name, which is nullable
		// (a learner may never have set one) — scan into a pointer so a
		// NULL there doesn't error the whole lookup.
		var learnerName *string
		var courseTitle string
		var issuedAt time.Time
		err := d.Pool.QueryRow(ctx, `SELECT * FROM verify_certificate($1)`, certificateID).Scan(&learnerName, &courseTitle, &issuedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "certificate not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"learner_name": learnerName,
			"course_title": courseTitle,
			"issued_at":    issuedAt,
		})
	}
}
