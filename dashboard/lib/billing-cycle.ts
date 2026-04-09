const BILLING_DAY = parseInt(process.env.NEXT_PUBLIC_BILLING_DAY || "10", 10);

export function currentCycle(): { since: string; until: string } {
  const now = new Date();
  const year = now.getFullYear();
  const month = now.getMonth(); // 0-indexed

  let cycleStart: Date;
  if (now.getDate() >= BILLING_DAY) {
    cycleStart = new Date(year, month, BILLING_DAY);
  } else {
    cycleStart = new Date(year, month - 1, BILLING_DAY);
  }

  const cycleEnd = new Date(cycleStart);
  cycleEnd.setMonth(cycleEnd.getMonth() + 1);
  cycleEnd.setDate(cycleEnd.getDate() - 1);

  return {
    since: fmt(cycleStart),
    until: fmt(cycleEnd),
  };
}

export function previousCycle(): { since: string; until: string } {
  const cur = currentCycle();
  const start = new Date(cur.since);
  start.setMonth(start.getMonth() - 1);
  const end = new Date(cur.since);
  end.setDate(end.getDate() - 1);
  return { since: fmt(start), until: fmt(end) };
}

function fmt(d: Date): string {
  return d.toISOString().slice(0, 10);
}
