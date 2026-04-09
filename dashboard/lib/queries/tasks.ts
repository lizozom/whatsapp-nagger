import { getDb } from "../db";
import type { Task } from "../types";

export function listTasks(assignee?: string, status?: string): Task[] {
  let sql = `SELECT id, content, assignee, status, COALESCE(due_date,'') AS due_date, created_at, updated_at FROM tasks`;
  const conditions: string[] = [];
  const args: unknown[] = [];

  if (assignee) {
    conditions.push("LOWER(assignee) = LOWER(?)");
    args.push(assignee);
  }
  if (status) {
    conditions.push("status = ?");
    args.push(status);
  }
  if (conditions.length > 0) {
    sql += " WHERE " + conditions.join(" AND ");
  }
  sql +=
    " ORDER BY CASE WHEN due_date IS NULL OR due_date = '' THEN 1 ELSE 0 END, due_date ASC, created_at DESC";

  return getDb().prepare(sql).all(...args) as Task[];
}

export interface AssigneeStats {
  assignee: string;
  pending: number;
  done: number;
  overdue: number;
}

export function taskStats() {
  const all = listTasks();
  const today = new Date().toISOString().slice(0, 10);
  const byAssignee: Record<string, AssigneeStats> = {};
  let totalDone = 0;
  let totalDays = 0;
  let daysCount = 0;

  for (const t of all) {
    if (!byAssignee[t.assignee]) {
      byAssignee[t.assignee] = {
        assignee: t.assignee,
        pending: 0,
        done: 0,
        overdue: 0,
      };
    }
    const s = byAssignee[t.assignee];

    if (t.status === "pending") {
      s.pending++;
      if (t.due_date && t.due_date < today) {
        s.overdue++;
      }
    } else if (t.status === "done") {
      s.done++;
      totalDone++;
      const created = new Date(t.created_at).getTime();
      const updated = new Date(t.updated_at).getTime();
      if (!isNaN(created) && !isNaN(updated)) {
        const days = Math.max(0, (updated - created) / (1000 * 60 * 60 * 24));
        totalDays += days;
        daysCount++;
      }
    }
  }

  return {
    byAssignee: Object.values(byAssignee),
    totalDone,
    avgDaysToComplete:
      daysCount > 0 ? Math.round((totalDays / daysCount) * 10) / 10 : 0,
    totalPending: all.filter((t) => t.status === "pending").length,
    totalOverdue: all.filter(
      (t) => t.status === "pending" && t.due_date && t.due_date < today,
    ).length,
  };
}
