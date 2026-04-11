import Link from "next/link";
import type { Cycle } from "@/lib/billing-cycle";

interface Props {
  cycles: Cycle[];
  activeId: string;
}

export function CycleSelector({ cycles, activeId }: Props) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {cycles.map((c, i) => {
        const isActive = c.id === activeId;
        const label = i === 0 ? `Current · ${c.label}` : c.label;
        return (
          <Link
            key={c.id}
            href={`/finances?cycle=${c.id}`}
            className={`px-3 py-1.5 text-xs rounded-md border transition-colors ${
              isActive
                ? "bg-foreground text-background border-foreground"
                : "bg-background text-foreground border-border hover:bg-muted"
            }`}
          >
            {label}
          </Link>
        );
      })}
    </div>
  );
}
