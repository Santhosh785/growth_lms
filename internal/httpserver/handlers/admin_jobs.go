package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
)

// This file implements Task 10's background-job dashboard: a read-only view of
// the Redis-backed asynq queues (internal/worker) — per-queue depth/latency
// counters and the recent failed (retry + archived) tasks an operator would
// triage. Platform-owner only (registerAdminAPIRoutes gates it). It uses an
// asynq.Inspector built at startup; if the worker/Redis is unreachable the
// handler reports 503 rather than 500 so the failure is legible as "job
// backend down".

// recentFailedPerQueue bounds how many retry/archived tasks are pulled per
// queue for the dashboard — enough to triage without paging all of Redis.
const recentFailedPerQueue = 25

// queueInfoView renders one asynq.QueueInfo.
func queueInfoView(q *asynq.QueueInfo) gin.H {
	return gin.H{
		"queue":           q.Queue,
		"size":            q.Size,
		"pending":         q.Pending,
		"active":          q.Active,
		"scheduled":       q.Scheduled,
		"retry":           q.Retry,
		"archived":        q.Archived,
		"completed":       q.Completed,
		"processed_today": q.Processed,
		"failed_today":    q.Failed,
		"latency_ms":      q.Latency.Milliseconds(),
		"memory_bytes":    q.MemoryUsage,
		"paused":          q.Paused,
	}
}

// failedTaskView renders one retry/archived asynq.TaskInfo, trimming the
// payload (which may hold PII) down to its type and error.
func failedTaskView(t *asynq.TaskInfo) gin.H {
	v := gin.H{
		"id":         t.ID,
		"queue":      t.Queue,
		"type":       t.Type,
		"state":      t.State.String(),
		"retried":    t.Retried,
		"max_retry":  t.MaxRetry,
		"last_error": t.LastErr,
	}
	if !t.LastFailedAt.IsZero() {
		v["last_failed_at"] = t.LastFailedAt
	}
	if !t.NextProcessAt.IsZero() {
		v["next_process_at"] = t.NextProcessAt
	}
	return v
}

// JobsDashboard is GET /api/admin/jobs (platform owner): per-queue stats plus
// recent failed tasks across all queues.
func JobsDashboard(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Inspector == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "job backend not configured"})
			return
		}
		queues, err := d.Inspector.Queues()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "job backend unavailable"})
			return
		}

		queueViews := make([]gin.H, 0, len(queues))
		failed := make([]gin.H, 0)
		var totalOpenFailures int
		for _, q := range queues {
			info, err := d.Inspector.GetQueueInfo(q)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "job backend unavailable"})
				return
			}
			queueViews = append(queueViews, queueInfoView(info))
			totalOpenFailures += info.Retry + info.Archived

			// Recent retry + archived tasks are the operator's triage list.
			for _, lister := range []func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error){
				d.Inspector.ListRetryTasks, d.Inspector.ListArchivedTasks,
			} {
				tasks, err := lister(q, asynq.PageSize(recentFailedPerQueue), asynq.Page(1))
				if err != nil {
					continue // a transient per-queue listing error shouldn't blank the whole dashboard
				}
				for _, t := range tasks {
					failed = append(failed, failedTaskView(t))
				}
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"queues":              queueViews,
			"recent_failed_tasks": failed,
			"open_failure_count":  totalOpenFailures,
		})
	}
}
