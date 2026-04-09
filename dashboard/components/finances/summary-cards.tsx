import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

interface Props {
  spent: number;
  charges: number;
  refunds: number;
  txCount: number;
  since: string;
  until: string;
}

export function SummaryCards({
  spent,
  charges,
  refunds,
  txCount,
  since,
  until,
}: Props) {
  return (
    <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium text-muted-foreground">
            Net Spent
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold">
            ₪{Math.round(spent).toLocaleString()}
          </div>
          <p className="text-xs text-muted-foreground">
            {since} to {until}
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium text-muted-foreground">
            Gross Charges
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold">
            ₪{Math.round(charges).toLocaleString()}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium text-muted-foreground">
            Refunds
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold text-green-600">
            ₪{Math.round(refunds).toLocaleString()}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium text-muted-foreground">
            Transactions
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold">{txCount}</div>
        </CardContent>
      </Card>
    </div>
  );
}
