"use client";

import { useMemo, useState } from "react";
import {
  ColumnDef,
  flexRender,
  getCoreRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  SortingState,
  useReactTable,
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

  const table = useReactTable({
    data: rows,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: {
      pagination: { pageSize: 25 },
    },
  });

  return (
    <div className="space-y-3">
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
