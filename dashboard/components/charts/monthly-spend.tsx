"use client";

import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
} from "recharts";
import type { MonthlySpend } from "@/lib/types";

export function MonthlySpendChart({ data }: { data: MonthlySpend[] }) {
  const sorted = [...data].reverse();

  return (
    <ResponsiveContainer width="100%" height={300}>
      <BarChart data={sorted}>
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis dataKey="month" fontSize={12} />
        <YAxis fontSize={12} tickFormatter={(v) => `${(v / 1000).toFixed(0)}k`} />
        <Tooltip
          formatter={(value) => [`₪${Number(value).toLocaleString()}`, "Spent"]}
        />
        <Bar dataKey="spent" fill="hsl(var(--primary))" radius={[4, 4, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
}
