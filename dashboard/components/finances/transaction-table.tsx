import type { Transaction } from "@/lib/types";
import { normalizeCategory } from "@/lib/categories";

export function TransactionTable({ data }: { data: Transaction[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-left text-muted-foreground">
            <th className="py-2 pr-2">Date</th>
            <th className="py-2 pr-2">Description</th>
            <th className="py-2 pr-2">Category</th>
            <th className="py-2 text-right">Amount</th>
          </tr>
        </thead>
        <tbody>
          {data.map((tx) => (
            <tr key={tx.id} className="border-b last:border-0">
              <td className="py-1.5 pr-2 whitespace-nowrap">
                {tx.posted_at}
              </td>
              <td className="py-1.5 pr-2 truncate max-w-[200px]" title={tx.description}>
                {tx.description}
              </td>
              <td className="py-1.5 pr-2 text-muted-foreground truncate max-w-[140px]">
                {normalizeCategory(tx.description, tx.category)}
              </td>
              <td className="py-1.5 text-right font-mono whitespace-nowrap">
                ₪{Math.abs(tx.amount_ils).toLocaleString(undefined, {
                  minimumFractionDigits: 0,
                  maximumFractionDigits: 0,
                })}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
