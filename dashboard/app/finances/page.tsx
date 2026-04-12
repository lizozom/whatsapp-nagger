import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { SummaryCards } from "@/components/finances/summary-cards";
import { TopMerchants } from "@/components/finances/top-merchants";
import { TransactionTable } from "@/components/finances/transaction-table";
import { AllTransactionsTable } from "@/components/finances/all-transactions-table";
import { CycleSelector } from "@/components/finances/cycle-selector";
import { MonthlySpendChart } from "@/components/charts/monthly-spend";
import { CategoryChart } from "@/components/charts/category-chart";
import { cycleFromId, recentCycles } from "@/lib/billing-cycle";
import {
  totals,
  sumByCategory,
  sumByMerchant,
  cycleSpendByCategory,
  topTransactions,
  allTransactionsInCycle,
} from "@/lib/queries/transactions";

export const dynamic = "force-dynamic";

interface Props {
  searchParams: Promise<{ cycle?: string }>;
}

export default async function FinancesPage({ searchParams }: Props) {
  const params = await searchParams;
  const cycles = recentCycles(6);
  const cycle = cycleFromId(params.cycle);

  const t = totals(cycle.since, cycle.until);
  const categories = sumByCategory(cycle.since, cycle.until);
  const merchants = sumByMerchant(cycle.since, cycle.until, 15);
  const cycleSpend = cycleSpendByCategory(12);
  const topTx = topTransactions(cycle.since, cycle.until, 20);
  const allTx = allTransactionsInCycle(cycle.since, cycle.until);

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <h1 className="text-2xl font-bold">Finances</h1>
        <CycleSelector cycles={cycles} activeId={cycle.id} />
      </div>

      <SummaryCards
        spent={t.spent_ils}
        charges={t.charges_ils}
        refunds={t.refunds_ils}
        txCount={t.tx_count}
        since={cycle.since}
        until={cycle.until}
      />

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Spend by Billing Cycle</CardTitle>
          </CardHeader>
          <CardContent>
            <MonthlySpendChart data={cycleSpend} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>By Category</CardTitle>
          </CardHeader>
          <CardContent>
            <CategoryChart data={categories} />
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Top Merchants</CardTitle>
          </CardHeader>
          <CardContent>
            <TopMerchants data={merchants} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Largest Charges</CardTitle>
          </CardHeader>
          <CardContent>
            <TransactionTable data={topTx} />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>All Transactions</CardTitle>
        </CardHeader>
        <CardContent>
          <AllTransactionsTable data={allTx} />
        </CardContent>
      </Card>
    </div>
  );
}
