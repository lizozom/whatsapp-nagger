import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { taskStats, listTasks } from "@/lib/queries/tasks";

export const dynamic = "force-dynamic";

function daysOverdue(dueDate: string): number {
  const due = new Date(dueDate);
  const now = new Date();
  return Math.floor((now.getTime() - due.getTime()) / (1000 * 60 * 60 * 24));
}

export default function TasksPage() {
  const stats = taskStats();
  const pending = listTasks(undefined, "pending");
  const done = listTasks(undefined, "done");

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Tasks</h1>

      <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Pending
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.totalPending}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Overdue
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold text-red-500">
              {stats.totalOverdue}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Completed
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{stats.totalDone}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Avg Days to Complete
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {stats.avgDaysToComplete}
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>By Assignee</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="space-y-3">
            {stats.byAssignee.map((a) => (
              <div
                key={a.assignee}
                className="flex items-center justify-between py-2 border-b last:border-0"
              >
                <span className="font-medium">{a.assignee}</span>
                <div className="flex gap-4 text-sm">
                  <span>{a.pending} pending</span>
                  {a.overdue > 0 && (
                    <span className="text-red-500">{a.overdue} overdue</span>
                  )}
                  <span className="text-muted-foreground">{a.done} done</span>
                </div>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Pending Tasks</CardTitle>
        </CardHeader>
        <CardContent>
          {pending.length === 0 ? (
            <p className="text-muted-foreground text-sm">No pending tasks. Miracle.</p>
          ) : (
            <div className="space-y-2">
              {pending.map((t) => {
                const overdue = t.due_date && daysOverdue(t.due_date) > 0;
                return (
                  <div
                    key={t.id}
                    className="flex items-start justify-between py-2 border-b last:border-0 gap-4"
                  >
                    <div className="min-w-0">
                      <p className="text-sm">{t.content}</p>
                      <p className="text-xs text-muted-foreground">
                        {t.assignee}
                        {t.due_date && (
                          <span className={overdue ? " text-red-500 font-medium" : ""}>
                            {" "}
                            &middot; due {t.due_date}
                            {overdue && ` (${daysOverdue(t.due_date)}d overdue)`}
                          </span>
                        )}
                      </p>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Completed Tasks</CardTitle>
        </CardHeader>
        <CardContent>
          {done.length === 0 ? (
            <p className="text-muted-foreground text-sm">Nothing completed yet.</p>
          ) : (
            <div className="space-y-2">
              {done.map((t) => (
                <div
                  key={t.id}
                  className="flex items-start justify-between py-2 border-b last:border-0 gap-4"
                >
                  <div className="min-w-0">
                    <p className="text-sm line-through text-muted-foreground">
                      {t.content}
                    </p>
                    <p className="text-xs text-muted-foreground">
                      {t.assignee} &middot; completed {t.updated_at.slice(0, 10)}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
