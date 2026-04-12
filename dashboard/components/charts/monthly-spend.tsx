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

function CustomTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;

  const items = payload
    .filter((p: any) => p.value > 0)
    .sort((a: any, b: any) => b.value - a.value);

  const total = items.reduce((s: number, p: any) => s + p.value, 0);

  return (
    <div className="bg-background border rounded-lg shadow-lg p-3 text-xs max-w-[220px]">
      <div className="font-medium mb-2 text-sm">{label}</div>
      <div className="space-y-1 max-h-[200px] overflow-y-auto">
        {items.map((item: any) => (
          <div key={item.name} className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-1.5 min-w-0">
              <span
                className="w-2.5 h-2.5 rounded-sm shrink-0"
                style={{ backgroundColor: item.color }}
              />
              <span className="truncate text-muted-foreground">{item.name}</span>
            </div>
            <span className="tabular-nums font-medium shrink-0">
              ₪{Math.round(item.value).toLocaleString()}
            </span>
          </div>
        ))}
      </div>
      <div className="border-t mt-2 pt-2 flex justify-between font-medium text-sm">
        <span>Total</span>
        <span className="tabular-nums">₪{Math.round(total).toLocaleString()}</span>
      </div>
    </div>
  );
}

export function MonthlySpendChart({ data }: { data: CycleSpendRow[] }) {
  const activeCategories = CATEGORY_ORDER.filter((cat) =>
    data.some((row) => (row[cat] as number) > 0),
  );

  return (
    <ResponsiveContainer width="100%" height={400}>
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
        <Tooltip content={<CustomTooltip />} />
        <Legend
          wrapperStyle={{ fontSize: 10, paddingTop: 8 }}
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
