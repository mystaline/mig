package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresDB struct {
	Pool *pgxpool.Pool
}

func (p *PostgresDB) Exec(
	ctx context.Context,
	query string,
	args ...any,
) error {
	_, err := p.Pool.Exec(ctx, query, args...)
	return err
}

func (p *PostgresDB) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) Row {
	return p.Pool.QueryRow(ctx, query, args...)
}

func (p *PostgresDB) Query(
	ctx context.Context,
	query string,
	args ...any,
) (Rows, error) {
	return p.Pool.Query(ctx, query, args...)
}

type pgxTx struct {
	tx  pgx.Tx
	ctx context.Context
}

func (t *pgxTx) Exec(ctx context.Context, query string, args ...any) error {
	_, err := t.tx.Exec(ctx, query, args...)
	return err
}

func (p *PostgresDB) ExecTx(ctx context.Context, fn func(Tx) error) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback(ctx)
		} else {
			err = tx.Commit(ctx)
		}
	}()

	err = formatPgError(fn(&pgxTx{tx: tx, ctx: ctx}))
	return err
}

func formatPgError(err error) error {
	if pgErr, ok := err.(*pgconn.PgError); ok {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("PostgreSQL Error: %s\n", pgErr.Message))
		if pgErr.Detail != "" {
			sb.WriteString(fmt.Sprintf("Detail: %s\n", pgErr.Detail))
		}
		if pgErr.Hint != "" {
			sb.WriteString(fmt.Sprintf("Hint: %s\n", pgErr.Hint))
		}
		if pgErr.Line > 0 {
			sb.WriteString(fmt.Sprintf("Line: %d\n", pgErr.Line))
		}
		return fmt.Errorf("\n%s", sb.String())
	}
	return err
}

func (db *PostgresDB) Close() {
	db.Pool.Close()
}

func NewPostgresDB(ctx context.Context, connStr string) (*PostgresDB, error) {
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("unable to parse connection string: %w", err)
	}
	return NewPostgresDBFromConfig(ctx, config)
}

func NewPostgresDBFromConfig(ctx context.Context, config *pgxpool.Config) (*PostgresDB, error) {
	// Wait for DB logic
	var pool *pgxpool.Pool
	host := config.ConnConfig.Host
	port := config.ConnConfig.Port
	var err error
	for i := 0; i < 10; i++ {
		pool, err = pgxpool.NewWithConfig(ctx, config)
		if err == nil {
			err = pool.Ping(ctx)
			if err == nil {
				return &PostgresDB{Pool: pool}, nil
			}
		}
		fmt.Printf("Waiting for database to be ready at %s:%d... (attempt %d/10)\n", host, port, i+1)
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("could not connect to database after 10 attempts: %w", err)
}

func CreateDatabase(ctx context.Context, config *pgx.ConnConfig, dbName string) error {
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName))
	return err
}

func DropDatabase(ctx context.Context, config *pgx.ConnConfig, dbName string) error {
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	// Ensure no other connections are open to the DB before dropping
	_, _ = conn.Exec(ctx, fmt.Sprintf(`
		SELECT pg_terminate_backend(pg_stat_activity.pid)
		FROM pg_stat_activity
		WHERE pg_stat_activity.datname = '%s'
		AND pid <> pg_backend_pid();
	`, dbName))

	_, err = conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName))
	return err
}
