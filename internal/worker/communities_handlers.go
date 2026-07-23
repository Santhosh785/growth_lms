package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
	"growth-lms/internal/notify"
)

// communityDeps bundles what the Task 7 notification handlers need. Like
// every worker handler they run at the pool's own admin privileges — there
// is no per-request caller to scope RLS to for a background job.
type communityDeps struct {
	pool          *pgxpool.Pool
	profiles      *models.ProfileRepo
	memberships   *models.MembershipRepo
	notifications *models.NotificationRepo
	prefs         *models.NotificationPreferenceRepo
	unsub         *models.UnsubscribeTokenRepo
	email         notify.EmailClient
	baseURL       string
}

// outboundNotification describes one notification to deliver to one recipient.
// render nil means in-app only. masterOnly skips the per-category email
// preference check (used for moderation alerts, which are not a user-
// subscribable category).
type outboundNotification struct {
	orgID       string
	recipientID string
	actorID     string
	typ         string
	category    string
	title       string
	inAppBody   string
	linkURL     string
	masterOnly  bool
	render      func(unsubscribeURL string) (subject, body string)
}

// dispatch writes the in-app row (always) then conditionally emails. A
// missing/opted-out recipient still succeeds so asynq does not retry forever.
func (cd *communityDeps) dispatch(ctx context.Context, n outboundNotification) error {
	var actorID *string
	if n.actorID != "" {
		actorID = &n.actorID
	}
	if _, err := cd.notifications.Create(ctx, cd.pool, n.orgID, n.recipientID, n.typ, n.title, n.inAppBody, n.linkURL, actorID); err != nil {
		return fmt.Errorf("worker: write in-app notification: %w", err)
	}

	if n.render == nil {
		return nil
	}

	profile, err := cd.profiles.GetByID(ctx, cd.pool, n.recipientID)
	if err != nil {
		// Recipient vanished (deleted account) — in-app row is harmless, no
		// email to send.
		return nil
	}
	if profile.NotificationOptOut {
		return nil
	}
	if !n.masterOnly {
		enabled, err := cd.prefs.IsEmailEnabled(ctx, cd.pool, n.recipientID, n.orgID, n.category)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
	}

	unsubscribeURL, err := cd.makeUnsubscribeURL(ctx, n.recipientID, n.orgID, n.category, n.masterOnly)
	if err != nil {
		return err
	}
	subject, body := n.render(unsubscribeURL)
	return cd.email.SendEmail(ctx, profile.Email, subject, body)
}

// makeUnsubscribeURL mints a one-shot token and builds the footer link. For
// masterOnly (moderation) mails the token has no category, so unsubscribing
// silences the whole org rather than a single category.
func (cd *communityDeps) makeUnsubscribeURL(ctx context.Context, userID, orgID, category string, masterOnly bool) (string, error) {
	token, err := cd.unsub.NewToken()
	if err != nil {
		return "", err
	}
	var catPtr *string
	if !masterOnly && category != "" {
		catPtr = &category
	}
	orgPtr := &orgID
	if err := cd.unsub.Create(ctx, cd.pool, token, userID, orgPtr, catPtr); err != nil {
		return "", err
	}
	return cd.baseURL + "/unsubscribe/" + token, nil
}

func handleNotifyMention(cd *communityDeps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p NotifyMentionPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("worker: unmarshal mention payload: %w", err)
		}
		link := cd.baseURL + p.LinkPath
		return cd.dispatch(ctx, outboundNotification{
			orgID: p.OrgID, recipientID: p.RecipientID, actorID: p.ActorID,
			typ: "mention", category: "mentions",
			title:     fmt.Sprintf("%s mentioned you in \"%s\"", p.ActorName, p.ThreadTitle),
			inAppBody: p.Preview, linkURL: link,
			render: func(u string) (string, string) {
				return notify.RenderMentionEmail(p.ActorName, p.ThreadTitle, p.Preview, link, u)
			},
		})
	}
}

func handleNotifyReply(cd *communityDeps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p NotifyReplyPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("worker: unmarshal reply payload: %w", err)
		}
		link := cd.baseURL + p.LinkPath
		return cd.dispatch(ctx, outboundNotification{
			orgID: p.OrgID, recipientID: p.RecipientID, actorID: p.ActorID,
			typ: "reply", category: "replies",
			title:     fmt.Sprintf("%s replied in \"%s\"", p.ActorName, p.ThreadTitle),
			inAppBody: p.Preview, linkURL: link,
			render: func(u string) (string, string) {
				return notify.RenderReplyEmail(p.ActorName, p.ThreadTitle, p.Preview, link, u)
			},
		})
	}
}

// handleNotifyReportFiled fans out to every moderator/owner of the org. These
// moderation alerts are in-app always and emailed only subject to the master
// opt-out (no per-category subscription).
func handleNotifyReportFiled(cd *communityDeps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p NotifyReportFiledPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("worker: unmarshal report payload: %w", err)
		}
		members, err := cd.memberships.ListByOrg(ctx, cd.pool, p.OrgID)
		if err != nil {
			return fmt.Errorf("worker: list org for report fan-out: %w", err)
		}
		link := cd.baseURL + p.LinkPath
		for _, m := range members {
			if m.Role != "owner" && m.Role != "moderator" {
				continue
			}
			err := cd.dispatch(ctx, outboundNotification{
				orgID: p.OrgID, recipientID: m.UserID,
				typ: "report_filed", masterOnly: true,
				title:     "A post was reported",
				inAppBody: fmt.Sprintf("Reason: %s", p.Reason), linkURL: link,
				render: func(u string) (string, string) {
					return notify.RenderBroadcastEmail("A post was reported",
						fmt.Sprintf("A community post was reported. Reason: %s", p.Reason), link, u)
				},
			})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// handleNotifyBroadcast fans out an owner/teacher announcement to every member.
func handleNotifyBroadcast(cd *communityDeps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p NotifyBroadcastPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("worker: unmarshal broadcast payload: %w", err)
		}
		members, err := cd.memberships.ListByOrg(ctx, cd.pool, p.OrgID)
		if err != nil {
			return fmt.Errorf("worker: list org for broadcast fan-out: %w", err)
		}
		link := ""
		if p.LinkPath != "" {
			link = cd.baseURL + p.LinkPath
		}
		for _, m := range members {
			err := cd.dispatch(ctx, outboundNotification{
				orgID: p.OrgID, recipientID: m.UserID, actorID: p.ActorID,
				typ: "broadcast", category: "broadcasts",
				title: p.Title, inAppBody: p.Body, linkURL: link,
				render: func(u string) (string, string) {
					return notify.RenderBroadcastEmail(p.Title, p.Body, link, u)
				},
			})
			if err != nil {
				return err
			}
		}
		return nil
	}
}
