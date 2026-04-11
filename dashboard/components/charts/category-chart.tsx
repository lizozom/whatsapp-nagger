"use client";

import {
  PieChart,
  Pie,
  Cell,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import type { SumRow } from "@/lib/types";
import { getCategoryColor } from "@/lib/categories";

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
          {top.map((row) => (
            <Cell key={row.key} fill={getCategoryColor(row.key)} />
          ))}
        </Pie>
        <Tooltip formatter={(value) => `₪${Number(value).toLocaleString()}`} />
      </PieChart>
    </ResponsiveContainer>
  );
}
