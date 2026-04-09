package api

import (
	"math"
	"net/http"
	"time"
)

// GET /api/tasks?assignee=&status=
func (rt *Router) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	assignee := r.URL.Query().Get("assignee")
	status := r.URL.Query().Get("status")

	tasks, err := rt.Tasks.ListTasks(assignee, status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"tasks": tasks,
		"count": len(tasks),
	})
}

type assigneeStats struct {
	Assignee string `json:"assignee"`
	Pending  int    `json:"pending"`
	Done     int    `json:"done"`
	Overdue  int    `json:"overdue"`
}

// parseTime tries multiple timestamp formats that SQLite CURRENT_TIMESTAMP may produce.
func parseTime(s string) time.Time {
	for _, fmt := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(fmt, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// GET /api/tasks/stats
func (rt *Router) handleTaskStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	all, err := rt.Tasks.ListTasks("", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	today := now.Format("2006-01-02")

	byAssignee := map[string]*assigneeStats{}
	var totalDays float64
	var daysCount int
	var totalDone int
	var recentCompletions []any

	for _, t := range all {
		s, ok := byAssignee[t.Assignee]
		if !ok {
			s = &assigneeStats{Assignee: t.Assignee}
			byAssignee[t.Assignee] = s
		}

		switch t.Status {
		case "pending":
			s.Pending++
			if t.DueDate != "" && t.DueDate < today {
				s.Overdue++
			}
		case "done":
			s.Done++
			totalDone++
			if totalDone <= 10 {
				recentCompletions = append(recentCompletions, t)
			}
			created := parseTime(t.CreatedAt)
			updated := parseTime(t.UpdatedAt)
			if !created.IsZero() && !updated.IsZero() {
				days := updated.Sub(created).Hours() / 24
				if days < 0 {
					days = 0
				}
				totalDays += days
				daysCount++
			}
		}
	}

	stats := make([]assigneeStats, 0, len(byAssignee))
	for _, s := range byAssignee {
		stats = append(stats, *s)
	}

	avgDays := 0.0
	if daysCount > 0 {
		avgDays = math.Round(totalDays/float64(daysCount)*10) / 10
	}

	writeJSON(w, map[string]any{
		"by_assignee":          stats,
		"avg_days_to_complete": avgDays,
		"total_done":           totalDone,
		"recent_completions":   recentCompletions,
	})
}
