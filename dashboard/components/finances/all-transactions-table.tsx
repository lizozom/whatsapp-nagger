"use client";

import { useMemo, useState } from "react";
import {
  ColumnDef,
  ColumnFiltersState,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  SortingState,
  useReactTable,
  Row as TableRowT,
} from "@tanstack/react-table";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Checkbox } from "@/components/ui/checkbox";
import type { Transaction } from "@/lib/types";
import { normalizeCategory } from "@/lib/categories";

interface Row extends Transaction {
  normalized_category: string;
}

const columns: ColumnDef<Row>[] = [
  {
    accessorKey: "posted_at",
    header: ({ column }) => (
      <SortHeader column={column} label="Date" />
    ),
    cell: ({ row }) => (
      <span className="whitespace-nowrap">{row.original.posted_at}</span>
    ),
  },
  {
    accessorKey: "description",
    header: ({ column }) => (
      <SortHeader column={column} label="Merchant" />
    ),
    cell: ({ row }) => (
      <span
        className="truncate block max-w-[220px]"
        title={row.original.description}
      >
        {row.original.description}
      </span>
    ),
  },
  {
    accessorKey: "normalized_category",
    header: ({ column }) => (
      <SortHeader column={column} label="Category" />
    ),
    cell: ({ row }) => (
      <span className="text-muted-foreground whitespace-nowrap">
        {row.original.normalized_category}
      </span>
    ),
    filterFn: (row: TableRowT<Row>, columnId: string, value: string[]) => {
      if (!value || value.length === 0) return true;
      return value.includes(row.getValue(columnId) as string);
    },
  },
  {
    accessorKey: "card_last4",
    header: ({ column }) => (
      <SortHeader column={column} label="Card" />
    ),
    cell: ({ row }) => (
      <span className="text-muted-foreground text-xs tabular-nums">
        {row.original.provider}/{row.original.card_last4}
      </span>
    ),
  },
  {
    accessorKey: "amount_ils",
    header: ({ column }) => (
      <SortHeader column={column} label="Amount" align="right" />
    ),
    cell: ({ row }) => {
      const a = row.original.amount_ils;
      const isRefund = a > 0;
      return (
        <span
          className={`text-right tabular-nums whitespace-nowrap block ${
            isRefund ? "text-green-600" : ""
          }`}
        >
          {isRefund ? "+" : ""}₪
          {Math.round(Math.abs(a)).toLocaleString()}
        </span>
      );
    },
  },
];

function SortHeader({
  column,
  label,
  align = "left",
}: {
  column: any;
  label: string;
  align?: "left" | "right";
}) {
  const sort = column.getIsSorted();
  return (
    <button
      type="button"
      onClick={() => column.toggleSorting(sort === "asc")}
      className={`flex items-center gap-1 text-xs font-medium hover:text-foreground ${
        align === "right" ? "ml-auto" : ""
      }`}
    >
      {label}
      {sort === "asc" && <span>↑</span>}
      {sort === "desc" && <span>↓</span>}
    </button>
  );
}

export function AllTransactionsTable({ data }: { data: Transaction[] }) {
  const rows = useMemo<Row[]>(
    () =>
      data.map((t) => ({
        ...t,
        normalized_category: normalizeCategory(t.description, t.category),
      })),
    [data],
  );

  const [sorting, setSorting] = useState<SortingState>([
    { id: "posted_at", desc: true },
  ]);
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);

  const table = useReactTable({
    data: rows,
    columns,
    state: { sorting, columnFilters },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: {
      pagination: { pageSize: 25 },
    },
  });

  const searchValue =
    (table.getColumn("description")?.getFilterValue() as string) ?? "";

  // All unique normalized categories that appear in the current dataset.
  const availableCategories = useMemo(() => {
    const set = new Set<string>();
    for (const r of rows) set.add(r.normalized_category);
    return [...set].sort();
  }, [rows]);

  const categoryFilter =
    (table.getColumn("normalized_category")?.getFilterValue() as string[]) ??
    [];

  function toggleCategory(cat: string) {
    const next = categoryFilter.includes(cat)
      ? categoryFilter.filter((c) => c !== cat)
      : [...categoryFilter, cat];
    table
      .getColumn("normalized_category")
      ?.setFilterValue(next.length > 0 ? next : undefined);
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-2">
        <input
          type="text"
          placeholder="Search merchant..."
          value={searchValue}
          onChange={(e) =>
            table.getColumn("description")?.setFilterValue(e.target.value)
          }
          className="flex-1 min-w-[150px] sm:max-w-xs px-3 py-2 border rounded-md text-sm"
        />
        <Popover>
          <PopoverTrigger className="inline-flex items-center justify-center gap-1.5 rounded-md border border-input bg-background hover:bg-muted px-3 py-2 text-sm font-medium transition-colors">
            Category
            {categoryFilter.length > 0 && (
              <span className="rounded-full bg-foreground text-background text-xs px-1.5 py-0.5 font-medium">
                {categoryFilter.length}
              </span>
            )}
          </PopoverTrigger>
          <PopoverContent className="w-56 p-2" align="start">
            <div className="space-y-1 max-h-80 overflow-y-auto">
              {availableCategories.map((cat) => (
                <label
                  key={cat}
                  className="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-muted cursor-pointer text-sm"
                >
                  <Checkbox
                    checked={categoryFilter.includes(cat)}
                    onCheckedChange={() => toggleCategory(cat)}
                  />
                  <span className="flex-1">{cat}</span>
                </label>
              ))}
              {categoryFilter.length > 0 && (
                <button
                  type="button"
                  className="w-full text-xs text-muted-foreground hover:text-foreground mt-1 py-1 border-t"
                  onClick={() =>
                    table
                      .getColumn("normalized_category")
                      ?.setFilterValue(undefined)
                  }
                >
                  Clear ({categoryFilter.length})
                </button>
              )}
            </div>
          </PopoverContent>
        </Popover>
      </div>
      <div className="rounded-md border overflow-hidden">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((hg) => (
              <TableRow key={hg.id}>
                {hg.headers.map((h) => (
                  <TableHead key={h.id}>
                    {h.isPlaceholder
                      ? null
                      : flexRender(
                          h.column.columnDef.header,
                          h.getContext(),
                        )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length > 0 ? (
              table.getRowModel().rows.map((row) => (
                <TableRow key={row.id}>
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className="h-24 text-center text-muted-foreground"
                >
                  No transactions in this cycle.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <div className="flex items-center justify-between gap-2 text-sm">
        <div className="text-muted-foreground">
          Page {table.getState().pagination.pageIndex + 1} of{" "}
          {table.getPageCount()} &middot; {rows.length} transactions
        </div>
        <div className="flex gap-1">
          <Button
            variant="outline"
            size="sm"
            onClick={() => table.previousPage()}
            disabled={!table.getCanPreviousPage()}
          >
            Previous
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => table.nextPage()}
            disabled={!table.getCanNextPage()}
          >
            Next
          </Button>
        </div>
      </div>
    </div>
  );
}
