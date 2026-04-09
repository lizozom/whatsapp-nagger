"use client";

import {
  PieChart,
  Pie,
  Cell,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import type { SumRow } from "@/lib/types";

const COLORS = [
  "hsl(220, 70%, 50%)",
  "hsl(150, 60%, 45%)",
  "hsl(30, 80%, 55%)",
  "hsl(350, 65%, 50%)",
  "hsl(260, 55%, 55%)",
  "hsl(180, 60%, 45%)",
  "hsl(45, 75%, 50%)",
  "hsl(0, 60%, 50%)",
  "hsl(200, 65%, 50%)",
  "hsl(100, 50%, 45%)",
];

export function CategoryChart({ data }: { data: SumRow[] }) {
  const top = data.filter((r) => r.spent_ils > 0).slice(0, 10);

  return (
    <ResponsiveContainer width="100%" height={350}>
      <PieChart>
        <Pie
          data={top}
          dataKey="spent_ils"
          nameKey="key"
          cx="50%"
          cy="50%"
          outerRadius={120}
          label={({ name, percent }: { name?: string; percent?: number }) =>
            `${(name ?? "").slice(0, 12)} ${((percent ?? 0) * 100).toFixed(0)}%`
          }
          labelLine={false}
          fontSize={11}
        >
          {top.map((_, i) => (
            <Cell key={i} fill={COLORS[i % COLORS.length]} />
          ))}
        </Pie>
        <Tooltip
          formatter={(value) => `₪${Number(value).toLocaleString()}`}
        />
      </PieChart>
    </ResponsiveContainer>
  );
}
