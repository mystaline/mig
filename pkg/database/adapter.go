package database

import "context"

type DB interface {
	Exec(ctx context.Context, query string, args ...any) error
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	ExecTx(ctx context.Context, fn func(Tx) error) error
	Close()
}

// Row is satisfied by both pgx.Row and *sql.Row.
type Row interface {
	Scan(dest ...any) error
}

// Rows is satisfied by both pgx.Rows and *sql.Rows (via sqliteRows wrapper).
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
}

type Tx interface {
	Exec(ctx context.Context, query string, args ...any) error
}
