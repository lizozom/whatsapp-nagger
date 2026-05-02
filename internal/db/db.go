package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type Task struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	Assignee  string `json:"assignee"`
	Status    string `json:"status"`
	DueDate   string `json:"due_date,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type TaskStore struct {
	db *sql.DB
}

func NewTaskStore(dbPath string) (*TaskStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	_, err = db.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			content TEXT NOT NULL,
			assignee TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			due_date TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT
		);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &TaskStore{db: db}, nil
}

// AddTask inserts a new task scoped to groupID.
func (s *TaskStore) AddTask(groupID, content, assignee, dueDate string) (*Task, error) {
	res, err := s.db.Exec(
		"INSERT INTO tasks (group_id, content, assignee, due_date) VALUES (?, ?, ?, ?)",
		groupID, content, assignee, dueDate,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}

	id, _ := res.LastInsertId()
	return s.getByID(groupID, id)
}

// ListTasks returns tasks scoped to groupID, optionally filtered by assignee/status.
func (s *TaskStore) ListTasks(groupID, assignee, status string) ([]Task, error) {
	query := "SELECT id, content, assignee, status, COALESCE(due_date,''), created_at, updated_at FROM tasks WHERE group_id = ?"
	args := []any{groupID}

	if assignee != "" {
		query += " AND LOWER(assignee) = LOWER(?)"
		args = append(args, assignee)
	}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY CASE WHEN due_date IS NULL OR due_date = '' THEN 1 ELSE 0 END, due_date ASC, created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Content, &t.Assignee, &t.Status, &t.DueDate, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// TaskUpdate is the partial-update payload for UpdateTask. Each field is
// applied iff non-empty; an empty field leaves the existing column untouched.
type TaskUpdate struct {
	Status   string
	DueDate  string
	Content  string
	Assignee string
}

// IsEmpty reports whether no field is set — callers can short-circuit a no-op
// update or the LLM-facing tool can refuse it.
func (u TaskUpdate) IsEmpty() bool {
	return u.Status == "" && u.DueDate == "" && u.Content == "" && u.Assignee == ""
}

// UpdateTask updates a task within groupID. Only sets fields that are non-empty.
// Returns the post-update task. If no fields are set, returns the current row
// unchanged (callers should use IsEmpty to detect the no-op case if relevant).
func (s *TaskStore) UpdateTask(groupID string, id int64, fields TaskUpdate) (*Task, error) {
	var setClauses []string
	var args []any

	if fields.Status != "" {
		setClauses = append(setClauses, "status = ?")
		args = append(args, fields.Status)
	}
	if fields.DueDate != "" {
		setClauses = append(setClauses, "due_date = ?")
		args = append(args, fields.DueDate)
	}
	if fields.Content != "" {
		setClauses = append(setClauses, "content = ?")
		args = append(args, fields.Content)
	}
	if fields.Assignee != "" {
		setClauses = append(setClauses, "assignee = ?")
		args = append(args, fields.Assignee)
	}
	if len(setClauses) == 0 {
		return s.getByID(groupID, id)
	}

	setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id, groupID)

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ? AND group_id = ?", strings.Join(setClauses, ", "))
	_, err := s.db.Exec(query, args...)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	return s.getByID(groupID, id)
}

// ReassignPending bulk-reassigns all pending tasks in groupID from oldName to
// newName. Done tasks keep their original assignee (historical record per
// Story 2.6). Returns the count of rows affected.
func (s *TaskStore) ReassignPending(groupID, oldName, newName string) (int64, error) {
	res, err := s.db.Exec(`
		UPDATE tasks
		SET assignee = ?, updated_at = CURRENT_TIMESTAMP
		WHERE group_id = ? AND assignee = ? AND status = 'pending'`,
		newName, groupID, oldName,
	)
	if err != nil {
		return 0, fmt.Errorf("reassign pending tasks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteTask removes a task within groupID. Returns an error if the task doesn't exist in that group.
func (s *TaskStore) DeleteTask(groupID string, id int64) error {
	res, err := s.db.Exec("DELETE FROM tasks WHERE id = ? AND group_id = ?", id, groupID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task %d not found", id)
	}
	return nil
}

// CountOverdueByAssignee returns counts of pending overdue tasks within groupID.
func (s *TaskStore) CountOverdueByAssignee(groupID, before string) (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT assignee, COUNT(*) FROM tasks
		 WHERE group_id = ? AND status = 'pending' AND due_date != '' AND due_date < ?
		 GROUP BY assignee`, groupID, before)
	if err != nil {
		return nil, fmt.Errorf("count overdue: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var assignee string
		var count int
		if err := rows.Scan(&assignee, &count); err != nil {
			return nil, fmt.Errorf("scan overdue row: %w", err)
		}
		counts[assignee] = count
	}
	return counts, rows.Err()
}

func (s *TaskStore) Close() error {
	return s.db.Close()
}

// GetMeta returns the metadata value for (groupID, key), or "" if absent.
func (s *TaskStore) GetMeta(groupID, key string) (string, error) {
	var value string
	err := s.db.QueryRow(
		"SELECT value FROM metadata WHERE group_id = ? AND key = ?",
		groupID, key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetMeta upserts the metadata value for (groupID, key). Requires the
// composite (group_id, key) PK installed by migrate_003.
func (s *TaskStore) SetMeta(groupID, key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO metadata (group_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(group_id, key) DO UPDATE SET value = ?`,
		groupID, key, value, value,
	)
	return err
}

func (s *TaskStore) getByID(groupID string, id int64) (*Task, error) {
	var t Task
	err := s.db.QueryRow(
		`SELECT id, content, assignee, status, COALESCE(due_date,''), created_at, updated_at
		 FROM tasks WHERE id = ? AND group_id = ?`,
		id, groupID,
	).Scan(&t.ID, &t.Content, &t.Assignee, &t.Status, &t.DueDate, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get task %d: %w", id, err)
	}
	return &t, nil
}
