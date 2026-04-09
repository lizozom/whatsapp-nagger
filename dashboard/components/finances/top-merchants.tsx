import type { SumRow } from "@/lib/types";

export function TopMerchants({ data }: { data: SumRow[] }) {
  return (
    <div className="space-y-2">
      {data
        .filter((r) => r.spent_ils > 0)
        .map((row, i) => (
          <div
            key={row.key}
            className="flex items-center justify-between py-1 border-b last:border-0"
          >
            <div className="flex items-center gap-2 min-w-0">
              <span className="text-sm text-muted-foreground w-5 shrink-0">
                {i + 1}.
              </span>
              <span className="text-sm truncate" title={row.key}>
                {row.key}
              </span>
            </div>
            <div className="text-right shrink-0 ml-2">
              <span className="text-sm font-medium">
                ₪{Math.round(row.spent_ils).toLocaleString()}
              </span>
              <span className="text-xs text-muted-foreground ml-1">
                ({row.tx_count})
              </span>
            </div>
          </div>
        ))}
    </div>
  );
}
