export interface Task {
  id: number;
  content: string;
  assignee: string;
  status: string;
  due_date: string;
  created_at: string;
  updated_at: string;
}

export interface Transaction {
  id: string;
  provider: string;
  card_last4: string;
  posted_at: string;
  amount_ils: number;
  description: string;
  memo: string;
  category: string;
  status: string;
}

export interface SumRow {
  key: string;
  tx_count: number;
  spent_ils: number;
  charges_ils: number;
  refunds_ils: number;
  category?: string; // normalized category (only populated for merchant groupings)
}

export interface MonthlySpend {
  month: string;
  spent: number;
  tx_count: number;
}
