package migrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mystaline/migration-tool/pkg/database"
)

type Migration struct {
	Version  string
	Name     string
	UpFile   string
	DownFile string
}

type Migrator struct {
	DB  *database.PostgresDB
	Dir string
}

func NewMigrator(db *database.PostgresDB, dir string) *Migrator {
	return &Migrator{
		DB:  db,
		Dir: dir,
	}
}

func (m *Migrator) Init(ctx context.Context) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			dirty BOOLEAN NOT NULL DEFAULT FALSE,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err := m.DB.Pool.Exec(ctx, query)
	return err
}

func (m *Migrator) GetAppliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := m.DB.Pool.Query(ctx, "SELECT version FROM schema_migrations WHERE dirty = FALSE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, nil
}

func (m *Migrator) CheckDirty(ctx context.Context) (bool, error) {
	var dirty bool
	err := m.DB.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE dirty = TRUE)").Scan(&dirty)
	return dirty, err
}

func (m *Migrator) ListMigrations() ([]Migration, error) {
	files, err := os.ReadDir(m.Dir)
	if err != nil {
		return nil, err
	}

	migrationsMap := make(map[string]*Migration)
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		parts := strings.SplitN(name, "_", 2)
		if len(parts) < 2 {
			continue
		}
		version := parts[0]

		if _, ok := migrationsMap[version]; !ok {
			migrationsMap[version] = &Migration{Version: version}
		}

		if strings.HasSuffix(name, ".up.sql") {
			migrationsMap[version].UpFile = name
			migrationsMap[version].Name = strings.TrimSuffix(parts[1], ".up.sql")
		} else if strings.HasSuffix(name, ".down.sql") {
			migrationsMap[version].DownFile = name
		}
	}

	var result []Migration
	for _, m := range migrationsMap {
		result = append(result, *m)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Version < result[j].Version
	})

	return result, nil
}

func (m *Migrator) RunUp(ctx context.Context, steps int) error {
	dirty, err := m.CheckDirty(ctx)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("database is in a dirty state; manual intervention required")
	}

	allMigrations, err := m.ListMigrations()
	if err != nil {
		return err
	}

	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return err
	}

	count := 0
	for _, mig := range allMigrations {
		if applied[mig.Version] {
			continue
		}

		fmt.Printf("==> Applying migration %s: %s\n", mig.Version, mig.Name)

		err := m.applyMigration(ctx, mig)
		if err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", mig.Version, err)
		}

		fmt.Printf("      Applied %s\n", mig.Version)
		count++
		if steps > 0 && count >= steps {
			break
		}
	}

	if count == 0 {
		fmt.Println("No pending migrations found.")
	} else {
		fmt.Printf("Successfully applied %d migrations.\n", count)
	}

	return nil
}

func (m *Migrator) RunDown(ctx context.Context, steps int) error {
	dirty, err := m.CheckDirty(ctx)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("database is in a dirty state; manual intervention required")
	}

	allMigrations, err := m.ListMigrations()
	if err != nil {
		return err
	}

	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return err
	}

	// Sort migrations in reverse for down
	sort.Slice(allMigrations, func(i, j int) bool {
		return allMigrations[i].Version > allMigrations[j].Version
	})

	count := 0
	for _, mig := range allMigrations {
		if !applied[mig.Version] {
			continue
		}

		fmt.Printf("==> Rolling back migration %s: %s\n", mig.Version, mig.Name)

		err := m.rollbackMigration(ctx, mig, allMigrations)
		if err != nil {
			return fmt.Errorf("failed to rollback migration %s: %w", mig.Version, err)
		}

		fmt.Printf("      Rolled back %s\n", mig.Version)
		count++
		if steps > 0 && count >= steps {
			break
		}
	}

	if count == 0 {
		fmt.Println("No migrations to rollback.")
	} else {
		fmt.Printf("Successfully rolled back %d migrations.\n", count)
	}

	return nil
}

type Status struct {
	Version string
	Name    string
	Applied bool
}

func (m *Migrator) GetStatus(ctx context.Context) ([]Status, error) {
	all, err := m.ListMigrations()
	if err != nil {
		return nil, err
	}

	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return nil, err
	}

	var status []Status
	for _, mig := range all {
		status = append(status, Status{
			Version: mig.Version,
			Name:    mig.Name,
			Applied: applied[mig.Version],
		})
	}
	return status, nil
}

func (m *Migrator) rollbackMigration(ctx context.Context, mig Migration, all []Migration) error {
	content, err := os.ReadFile(filepath.Join(m.Dir, mig.DownFile))
	if err != nil {
		return err
	}

	return m.DB.ExecTx(ctx, func(tx pgx.Tx) error {
		// Set dirty
		_, err := tx.Exec(ctx, "UPDATE schema_migrations SET dirty = TRUE WHERE version = $1", mig.Version)
		if err != nil {
			return err
		}

		// Run SQL
		_, err = tx.Exec(ctx, string(content))
		if err != nil {
			return formatPgError(err)
		}

		// Delete record
		_, err = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", mig.Version)
		return err
	})
}

func (m *Migrator) applyMigration(ctx context.Context, mig Migration) error {
	content, err := os.ReadFile(filepath.Join(m.Dir, mig.UpFile))
	if err != nil {
		return err
	}

	// 1. Mark as dirty BEFORE running the migration script.
	// We do this in a separate call to ensure it persists even if the main script fails.
	_, err = m.DB.Pool.Exec(ctx, "INSERT INTO schema_migrations (version, dirty) VALUES ($1, TRUE) ON CONFLICT (version) DO UPDATE SET dirty = TRUE", mig.Version)
	if err != nil {
		return fmt.Errorf("failed to mark migration as dirty: %w", err)
	}

	// 2. Run the migration script in a transaction.
	err = m.DB.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err = tx.Exec(ctx, string(content))
		return formatPgError(err)
	})
	if err != nil {
		return err
	}

	// 3. Mark as clean AFTER successful migration.
	_, err = m.DB.Pool.Exec(ctx, "UPDATE schema_migrations SET dirty = FALSE WHERE version = $1", mig.Version)
	if err != nil {
		return fmt.Errorf("failed to mark migration as clean: %w", err)
	}

	return nil
}

func (m *Migrator) Create(name string) error {
	timestamp := time.Now().Format("20060102150405")
	safeName := strings.ReplaceAll(strings.ToLower(name), " ", "_")

	upName := fmt.Sprintf("%s_%s.up.sql", timestamp, safeName)
	downName := fmt.Sprintf("%s_%s.down.sql", timestamp, safeName)

	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}

	upPath := filepath.Join(m.Dir, upName)
	downPath := filepath.Join(m.Dir, downName)

	if err := os.WriteFile(upPath, []byte("-- Up migration\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(downPath, []byte("-- Down migration\n"), 0o644); err != nil {
		return err
	}

	fmt.Printf("Created migration files:\n  %s\n  %s\n", upPath, downPath)
	return nil
}

func (m *Migrator) Repair(ctx context.Context) error {
	var version string
	err := m.DB.Pool.QueryRow(ctx, "SELECT version FROM schema_migrations WHERE dirty = TRUE").Scan(&version)
	if err != nil {
		return fmt.Errorf("no dirty migration found to repair: %w", err)
	}

	fmt.Printf("==> Repairing dirty migration: %s\n", version)

	all, err := m.ListMigrations()
	if err != nil {
		return err
	}

	var targetMig *Migration
	for _, mig := range all {
		if mig.Version == version {
			targetMig = &mig
			break
		}
	}

	if targetMig == nil || targetMig.DownFile == "" {
		return fmt.Errorf("cannot repair version %s: down migration file not found", version)
	}

	fmt.Printf("==> Running rollback (down) for version %s to reach previous stable state...\n", version)

	// Execute the rollback
	content, err := os.ReadFile(filepath.Join(m.Dir, targetMig.DownFile))
	if err != nil {
		return err
	}

	err = m.DB.ExecTx(ctx, func(tx pgx.Tx) error {
		_, err = tx.Exec(ctx, string(content))
		return formatPgError(err)
	})
	if err != nil {
		return fmt.Errorf("failed to execute rollback for repair: %w", err)
	}

	// Remove the record from schema_migrations
	_, err = m.DB.Pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", version)
	if err != nil {
		return fmt.Errorf("failed to clear dirty record after repair: %w", err)
	}

	fmt.Printf("Successfully repaired and rolled back to version before %s.\n", version)
	return nil
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
