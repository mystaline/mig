package database

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type SQLiteDB struct {
	db *sql.DB
}

type sqliteTx struct {
	tx *sql.Tx
}

func (t *sqliteTx) Exec(ctx context.Context, query string, args ...any) error {
	_, err := t.tx.ExecContext(ctx, query, args...)
	return err
}

// sqliteRows wraps *sql.Rows to satisfy Rows (Close with no return value).
type sqliteRows struct {
	rows *sql.Rows
}

func (r *sqliteRows) Next() bool             { return r.rows.Next() }
func (r *sqliteRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *sqliteRows) Close()                 { r.rows.Close() }

// NewSQLiteDB opens a SQLite database at the given file path.
// The path can be a file path or ":memory:" for an in-memory database.
func NewSQLiteDB(_ context.Context, path string) (*SQLiteDB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("unable to open sqlite database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("unable to ping sqlite database: %w", err)
	}
	return &SQLiteDB{db: db}, nil
}

func (s *SQLiteDB) Exec(ctx context.Context, query string, args ...any) error {
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteDB) QueryRow(ctx context.Context, query string, args ...any) Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

func (s *SQLiteDB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqliteRows{rows: rows}, nil
}

func (s *SQLiteDB) ExecTx(ctx context.Context, fn func(Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(&sqliteTx{tx: tx})
	return err
}

func (s *SQLiteDB) Close() {
	s.db.Close()
}
