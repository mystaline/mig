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

		if dbURL != "" {
			// Validate Postgres URL schema
			if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
				return fmt.Errorf("invalid DB_URL: must start with 'postgres://' or 'postgresql://'")
			}
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
		db, err := database.NewPostgresDB(ctx, dbURL)
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
		db, err := database.NewPostgresDB(ctx, dbURL)
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
		db, err := database.NewPostgresDB(ctx, dbURL)
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
		db, err := database.NewPostgresDB(ctx, dbURL)
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

		// 1. Generate unique DB name
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		letters := []rune("abcdefghijklmnopqrstuvwxyz")
		b := make([]rune, 6)
		for i := range b {
			b[i] = letters[rng.Intn(len(letters))]
		}
		tempDBName := fmt.Sprintf("mig_test_%s_%s", time.Now().Format("200601021504"), string(b))

		// 2. Prepare base connection string (connecting to 'postgres')
		poolConfig, err := pgxpool.ParseConfig(dbURL)
		if err != nil {
			return err
		}
		originalDB := poolConfig.ConnConfig.Database

		// Create admin config for create/drop
		adminConfig := poolConfig.ConnConfig.Copy()
		adminConfig.Database = "postgres"

		fmt.Printf("==> Creating temporary test database: %s\n", tempDBName)
		if err := database.CreateDatabase(ctx, adminConfig, tempDBName); err != nil {
			return fmt.Errorf("failed to create test database: %w", err)
		}

		// Ensure cleanup on failure or exit
		defer func() {
			fmt.Printf("\n==> Cleaning up...\n")
			fmt.Printf("==> Dropping temporary test database: %s\n", tempDBName)
			if err := database.DropDatabase(ctx, adminConfig, tempDBName); err != nil {
				fmt.Printf("Warning: failed to drop test database: %v\n", err)
			}
		}()

		// 3. Connect to the new temp DB
		testPoolConfig, _ := pgxpool.ParseConfig(dbURL)
		testPoolConfig.ConnConfig.Database = tempDBName

		db, err := database.NewPostgresDBFromConfig(ctx, testPoolConfig)
		if err != nil {
			return err
		}
		defer db.Close()

		m := migrator.NewMigrator(db, dir)

		// 4. Run up -> down -> up sequence
		fmt.Println("==> Step 1: Initializing and running all migrations (UP)")
		if err := m.Init(ctx); err != nil {
			return err
		}
		if err := m.RunUp(ctx, 0); err != nil {
			fmt.Println("\n[!] Test failed during Step 1. Attempting auto-repair for temporary test database (rollback)...")
			_ = m.Repair(ctx)
			return err
		}
		fmt.Println("      Step 1 OK")

		fmt.Println("\n==> Step 2: Rolling back all migrations (DOWN)")
		if err := m.RunDown(ctx, 0); err != nil {
			fmt.Println("\n[!] Test failed during Step 2. Attempting auto-repair for temporary test database (rollback)...")
			_ = m.Repair(ctx)
			return err
		}
		fmt.Println("      Step 2 OK")

		fmt.Println("\n==> Step 3: Re-applying all migrations (UP)")
		if err := m.RunUp(ctx, 0); err != nil {
			fmt.Println("\n[!] Test failed during Step 3. Attempting auto-repair for temporary test database (rollback)...")
			_ = m.Repair(ctx)
			return err
		}
		fmt.Println("      Step 3 OK")

		fmt.Printf("\n  Integrity test PASSED for %s (using temporary DB %s)\n", originalDB, tempDBName)
		return nil
	},
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
