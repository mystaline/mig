package cli

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/mystaline/migration-tool/pkg/database"
	"github.com/mystaline/migration-tool/pkg/migrator"
	"github.com/spf13/cobra"
)

var (
	dbURL string
	dir   string
)

// openDB detects the driver from the URL scheme and returns the appropriate DB.
// Supported schemes:
//   - postgres:// or postgresql:// → PostgresDB
//   - sqlite:// or sqlite3://      → SQLiteDB (path after scheme)
//   - file path (no scheme)        → SQLiteDB
func openDB(ctx context.Context, url string) (database.DB, error) {
	switch {
	case strings.HasPrefix(url, "postgres://"), strings.HasPrefix(url, "postgresql://"):
		return database.NewPostgresDB(ctx, url)
	case strings.HasPrefix(url, "sqlite://"):
		return database.NewSQLiteDB(ctx, strings.TrimPrefix(url, "sqlite://"))
	case strings.HasPrefix(url, "sqlite3://"):
		return database.NewSQLiteDB(ctx, strings.TrimPrefix(url, "sqlite3://"))
	default:
		return nil, fmt.Errorf("unsupported DB_URL scheme: %q (expected postgres://, postgresql://, or sqlite://)", url)
	}
}

var rootCmd = &cobra.Command{
	Use:   "mig",
	Short: "A robust database migration tool",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		_ = godotenv.Load()
		if dbURL == "" {
			dbURL = os.Getenv("DB_URL")
		}
		if dbURL == "" && cmd.Name() != "create" {
			return fmt.Errorf("DB_URL is not set. Please provide it via --db-url flag or DB_URL environment variable")
		}

		if dir == "" {
			dir = os.Getenv("MIGRATIONS_DIR")
			if dir == "" {
				dir = "./migrations"
			}
		}

		// Create migrations directory if we are running 'create' or 'init',
		// but for 'up', 'down', 'status', it MUST exist.
		if cmd.Name() != "create" && cmd.Name() != "init" {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				return fmt.Errorf("migrations directory '%s' does not exist. Current working directory: %s", dir, os.Getenv("PWD"))
			}
		}
		return nil
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the migration tracking table",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		db, err := openDB(ctx, dbURL)
		if err != nil {
			return err
		}
		defer db.Close()

		m := migrator.NewMigrator(db, dir)
		if err := m.Init(ctx); err != nil {
			return err
		}
		fmt.Println("Successfully initialized migrations table.")
		return nil
	},
}

var createCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new migration file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m := migrator.NewMigrator(nil, dir)
		return m.Create(args[0])
	},
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Apply pending migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		db, err := openDB(ctx, dbURL)
		if err != nil {
			return err
		}
		defer db.Close()

		m := migrator.NewMigrator(db, dir)
		return m.RunUp(ctx, 0)
	},
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Rollback last applied migration",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		db, err := openDB(ctx, dbURL)
		if err != nil {
			return err
		}
		defer db.Close()

		m := migrator.NewMigrator(db, dir)
		return m.RunDown(ctx, 1)
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show migration status",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		db, err := openDB(ctx, dbURL)
		if err != nil {
			return err
		}
		defer db.Close()

		m := migrator.NewMigrator(db, dir)
		status, err := m.GetStatus(ctx)
		if err != nil {
			return err
		}

		fmt.Printf("%-20s | %-30s | %-10s\n", "Version", "Name", "Status")
		fmt.Println(strings.Repeat("-", 66))
		for _, s := range status {
			statStr := "Pending"
			if s.Applied {
				statStr = "Applied"
			}
			fmt.Printf("%-20s | %-30s | %-10s\n", s.Version, s.Name, statStr)
		}
		return nil
	},
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run integrity test (up -> down -> up) on a temporary database",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		var (
			db      database.DB
			label   string
			cleanup func()
			err     error
		)

		switch {
		case strings.HasPrefix(dbURL, "postgres://"), strings.HasPrefix(dbURL, "postgresql://"):
			db, label, cleanup, err = setupPostgresTestDB(ctx)
		case strings.HasPrefix(dbURL, "sqlite://"), strings.HasPrefix(dbURL, "sqlite3://"):
			db, label, cleanup, err = setupSQLiteTestDB(ctx)
		default:
			return fmt.Errorf("unsupported DB_URL scheme for test command")
		}
		if err != nil {
			return err
		}
		defer cleanup()
		defer db.Close()

		m := migrator.NewMigrator(db, dir)

		fmt.Println("==> Step 1: Initializing and running all migrations (UP)")
		if err := m.Init(ctx); err != nil {
			return err
		}
		if err := m.RunUp(ctx, 0); err != nil {
			fmt.Println("\n[!] Test failed during Step 1. Attempting auto-repair...")
			_ = m.Repair(ctx)
			return err
		}
		fmt.Println("      Step 1 OK")

		fmt.Println("\n==> Step 2: Rolling back all migrations (DOWN)")
		if err := m.RunDown(ctx, 0); err != nil {
			fmt.Println("\n[!] Test failed during Step 2. Attempting auto-repair...")
			_ = m.Repair(ctx)
			return err
		}
		fmt.Println("      Step 2 OK")

		fmt.Println("\n==> Step 3: Re-applying all migrations (UP)")
		if err := m.RunUp(ctx, 0); err != nil {
			fmt.Println("\n[!] Test failed during Step 3. Attempting auto-repair...")
			_ = m.Repair(ctx)
			return err
		}
		fmt.Println("      Step 3 OK")

		fmt.Printf("\n  Integrity test PASSED (using %s)\n", label)
		return nil
	},
}

func setupPostgresTestDB(ctx context.Context) (database.DB, string, func(), error) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	letters := []rune("abcdefghijklmnopqrstuvwxyz")
	b := make([]rune, 6)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	tempDBName := fmt.Sprintf("mig_test_%s_%s", time.Now().Format("200601021504"), string(b))

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, "", nil, err
	}
	originalDB := poolConfig.ConnConfig.Database

	adminConfig := poolConfig.ConnConfig.Copy()
	adminConfig.Database = "postgres"

	fmt.Printf("==> Creating temporary test database: %s\n", tempDBName)
	if err := database.CreateDatabase(ctx, adminConfig, tempDBName); err != nil {
		return nil, "", nil, fmt.Errorf("failed to create test database: %w", err)
	}

	testPoolConfig, _ := pgxpool.ParseConfig(dbURL)
	testPoolConfig.ConnConfig.Database = tempDBName

	db, err := database.NewPostgresDBFromConfig(ctx, testPoolConfig)
	if err != nil {
		_ = database.DropDatabase(ctx, adminConfig, tempDBName)
		return nil, "", nil, err
	}

	cleanup := func() {
		fmt.Printf("\n==> Cleaning up...\n")
		fmt.Printf("==> Dropping temporary test database: %s\n", tempDBName)
		if err := database.DropDatabase(ctx, adminConfig, tempDBName); err != nil {
			fmt.Printf("Warning: failed to drop test database: %v\n", err)
		}
	}

	label := fmt.Sprintf("%s (temp DB: %s)", originalDB, tempDBName)
	return db, label, cleanup, nil
}

func setupSQLiteTestDB(ctx context.Context) (database.DB, string, func(), error) {
	tmpFile, err := os.CreateTemp("", "mig_test_*.sqlite")
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to create temp sqlite file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	db, err := database.NewSQLiteDB(ctx, tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return nil, "", nil, err
	}

	cleanup := func() {
		fmt.Printf("\n==> Cleaning up temporary SQLite file: %s\n", tmpPath)
		if err := os.Remove(tmpPath); err != nil {
			fmt.Printf("Warning: failed to remove temp sqlite file: %v\n", err)
		}
	}

	return db, fmt.Sprintf("SQLite temp file: %s", tmpPath), cleanup, nil
}

func Execute() {
	rootCmd.PersistentFlags().StringVar(&dbURL, "db-url", "", "Database connection URL")
	rootCmd.PersistentFlags().StringVar(&dir, "dir", "", "Directory containing migration files")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(testCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
