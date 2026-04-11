import type { SumRow } from "@/lib/types";

export function TopMerchants({ data }: { data: SumRow[] }) {
  return (
    <div className="space-y-2">
      {data
        .filter((r) => r.spent_ils > 0)
        .map((row, i) => (
          <div
            key={row.key}
            className="flex items-center justify-between py-1 border-b last:border-0 gap-2"
          >
            <div className="flex items-center gap-2 min-w-0 flex-1">
              <span className="text-sm text-muted-foreground w-5 shrink-0">
                {i + 1}.
              </span>
              <div className="min-w-0 flex-1">
                <div className="text-sm truncate" title={row.key}>
                  {row.key}
                </div>
                {row.category && (
                  <div className="text-xs text-muted-foreground truncate">
                    {row.category}
                  </div>
                )}
              </div>
            </div>
            <div className="text-right shrink-0">
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
