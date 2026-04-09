import Database from "better-sqlite3";

const DB_PATH = process.env.DB_PATH || "/data/tasks.db";

let _db: Database.Database | null = null;

export function getDb(): Database.Database {
  if (!_db) {
    _db = new Database(DB_PATH, { readonly: true });
    _db.pragma("journal_mode = WAL");
  }
  return _db;
}
