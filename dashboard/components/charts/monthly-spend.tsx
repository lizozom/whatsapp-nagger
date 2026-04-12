"use client";

import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  Legend,
} from "recharts";
import type { CycleSpendRow } from "@/lib/queries/transactions";
import { categoryColors, Category } from "@/lib/categories";

// Fixed order for stacking — most common first so they're at the bottom.
const CATEGORY_ORDER = [
  Category.Groceries,
  Category.Restaurants,
  Category.Transport,
  Category.Healthcare,
  Category.Home,
  Category.Kids,
  Category.Pets,
  Category.Leisure,
  Category.Fashion,
  Category.Insurance,
  Category.Electronics,
  Category.Municipality,
  Category.Travel,
  Category.Finance,
  Category.BooksMedia,
  Category.Beauty,
  Category.Work,
  Category.Other,
];

export function MonthlySpendChart({ data }: { data: CycleSpendRow[] }) {
  // Find which categories actually have data across all cycles.
  const activeCategories = CATEGORY_ORDER.filter((cat) =>
    data.some((row) => (row[cat] as number) > 0),
  );

  return (
    <ResponsiveContainer width="100%" height={350}>
      <BarChart data={data}>
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis
          dataKey="cycle"
          fontSize={10}
          interval={0}
          angle={-35}
          textAnchor="end"
          height={60}
        />
        <YAxis
          fontSize={12}
          tickFormatter={(v) => `${(v / 1000).toFixed(0)}k`}
        />
        <Tooltip
          formatter={(value, name) => [
            `₪${Math.round(Number(value)).toLocaleString()}`,
            String(name),
          ]}
          labelFormatter={(label) => `Cycle: ${label}`}
        />
        <Legend
          wrapperStyle={{ fontSize: 10 }}
          iconSize={8}
        />
        {activeCategories.map((cat) => (
          <Bar
            key={cat}
            dataKey={cat}
            stackId="spend"
            fill={categoryColors[cat]}
            name={cat}
          />
        ))}
      </BarChart>
    </ResponsiveContainer>
  );
}
