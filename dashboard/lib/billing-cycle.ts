const BILLING_DAY = parseInt(process.env.NEXT_PUBLIC_BILLING_DAY || "10", 10);

export interface Cycle {
  id: string; // start date, used as URL param, e.g. "2026-03-10"
  since: string;
  until: string;
  label: string; // human-friendly, e.g. "Mar 10 – Apr 9"
}

function fmt(d: Date): string {
  return d.toISOString().slice(0, 10);
}

function buildCycle(start: Date): Cycle {
  const end = new Date(start);
  end.setMonth(end.getMonth() + 1);
  end.setDate(end.getDate() - 1);
  return {
    id: fmt(start),
    since: fmt(start),
    until: fmt(end),
    label: `${formatLabel(start)} – ${formatLabel(end)}`,
  };
}

function formatLabel(d: Date): string {
  const months = [
    "Jan",
    "Feb",
    "Mar",
    "Apr",
    "May",
    "Jun",
    "Jul",
    "Aug",
    "Sep",
    "Oct",
    "Nov",
    "Dec",
  ];
  return `${d.getDate()} ${months[d.getMonth()]}`;
}

export function currentCycle(): Cycle {
  const now = new Date();
  const year = now.getFullYear();
  const month = now.getMonth();

  let cycleStart: Date;
  if (now.getDate() >= BILLING_DAY) {
    cycleStart = new Date(year, month, BILLING_DAY);
  } else {
    cycleStart = new Date(year, month - 1, BILLING_DAY);
  }
  return buildCycle(cycleStart);
}

/** Return the most recent `count` cycles (current first). */
export function recentCycles(count = 6): Cycle[] {
  const cycles: Cycle[] = [];
  const start = new Date(currentCycle().since);
  for (let i = 0; i < count; i++) {
    const d = new Date(start);
    d.setMonth(d.getMonth() - i);
    cycles.push(buildCycle(d));
  }
  return cycles;
}

/** Parse a cycle id ("YYYY-MM-DD" = start date) into a Cycle. Falls back to current. */
export function cycleFromId(id: string | undefined): Cycle {
  if (!id) return currentCycle();
  const d = new Date(id);
  if (isNaN(d.getTime())) return currentCycle();
  return buildCycle(d);
}
