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

func (s *TaskStore) AddTask(content, assignee, dueDate string) (*Task, error) {
	res, err := s.db.Exec(
		"INSERT INTO tasks (content, assignee, due_date) VALUES (?, ?, ?)",
		content, assignee, dueDate,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}

	id, _ := res.LastInsertId()
	return s.getByID(id)
}

func (s *TaskStore) ListTasks(assignee, status string) ([]Task, error) {
	query := "SELECT id, content, assignee, status, COALESCE(due_date,''), created_at, updated_at FROM tasks"
	var conditions []string
	var args []any

	if assignee != "" {
		conditions = append(conditions, "LOWER(assignee) = LOWER(?)")
		args = append(args, assignee)
	}
	if status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, status)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
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

func (s *TaskStore) UpdateTask(id int64, status, dueDate string) (*Task, error) {
	var setClauses []string
	var args []any

	if status != "" {
		setClauses = append(setClauses, "status = ?")
		args = append(args, status)
	}
	if dueDate != "" {
		setClauses = append(setClauses, "due_date = ?")
		args = append(args, dueDate)
	}
	if len(setClauses) == 0 {
		return s.getByID(id)
	}

	setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	_, err := s.db.Exec(query, args...)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	return s.getByID(id)
}

func (s *TaskStore) DeleteTask(id int64) error {
	res, err := s.db.Exec("DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task %d not found", id)
	}
	return nil
}

// CountOverdueByAssignee returns the number of pending tasks with a due date
// strictly before `before` (YYYY-MM-DD), grouped by assignee.
func (s *TaskStore) CountOverdueByAssignee(before string) (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT assignee, COUNT(*) FROM tasks
		 WHERE status = 'pending' AND due_date != '' AND due_date < ?
		 GROUP BY assignee`, before)
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

func (s *TaskStore) GetMeta(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM metadata WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *TaskStore) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO metadata (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
		key, value, value,
	)
	return err
}

func (s *TaskStore) getByID(id int64) (*Task, error) {
	var t Task
	err := s.db.QueryRow(
		"SELECT id, content, assignee, status, COALESCE(due_date,''), created_at, updated_at FROM tasks WHERE id = ?",
		id,
	).Scan(&t.ID, &t.Content, &t.Assignee, &t.Status, &t.DueDate, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get task %d: %w", id, err)
	}
	return &t, nil
}
