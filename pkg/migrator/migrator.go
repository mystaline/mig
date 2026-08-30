package migrator

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mystaline/mig/pkg/database"
)

type Migration struct {
	Version  string
	Name     string
	UpFile   string
	DownFile string
}

type Migrator struct {
	DB    database.DB
	Dir   string
	Quiet bool  // when true, suppresses progress output (for library mode over MCP stdio)
	fsys  fs.FS // optional; when set, reads migrations from fsys instead of disk
}

func NewMigrator(db database.DB, dir string) *Migrator {
	return &Migrator{
		DB:  db,
		Dir: dir,
	}
}

// NewMigratorFromFS creates a Migrator that reads migration files from an
// fs.FS (e.g. embed.FS) instead of the OS filesystem. The dir parameter is
// the path within the fs.FS to the migration files. DB must be set via
// SetDB or the Migrator.DB field before calling RunUp/RunDown.
func NewMigratorFromFS(fsys fs.FS, dir string) *Migrator {
	return &Migrator{
		fsys: fsys,
		Dir:  dir,
	}
}

// SetDB sets the database connection on the Migrator. Separated from
// NewMigratorFromFS because the DB may not be available at construction time
// (e.g. when using embed.FS in a library that opens the DB later).
func (m *Migrator) SetDB(db database.DB) {
	m.DB = db
}

func (m *Migrator) Init(ctx context.Context) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			dirty BOOLEAN NOT NULL DEFAULT FALSE,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`
	return m.DB.Exec(ctx, query)
}

func (m *Migrator) GetAppliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := m.DB.Query(ctx, "SELECT version FROM schema_migrations WHERE dirty = FALSE")
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
	err := m.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE dirty = TRUE)").Scan(&dirty)
	return dirty, err
}

func (m *Migrator) ListMigrations() ([]Migration, error) {
	var entries []fs.DirEntry
	var err error

	if m.fsys != nil {
		entries, err = fs.ReadDir(m.fsys, m.Dir)
	} else {
		entries, err = os.ReadDir(m.Dir)
	}
	if err != nil {
		return nil, err
	}

	migrationsMap := make(map[string]*Migration)
	for _, f := range entries {
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

		if !m.Quiet {
			fmt.Printf("==> Applying migration %s: %s\n", mig.Version, mig.Name)
		}

		err := m.applyMigration(ctx, mig)
		if err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", mig.Version, err)
		}

		if !m.Quiet {
			fmt.Printf("      Applied %s\n", mig.Version)
		}
		count++
		if steps > 0 && count >= steps {
			break
		}
	}

	if count == 0 {
		if !m.Quiet {
			fmt.Println("No pending migrations found.")
		}
	} else {
		if !m.Quiet {
			fmt.Printf("Successfully applied %d migrations.\n", count)
		}
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

		if !m.Quiet {
			fmt.Printf("==> Rolling back migration %s: %s\n", mig.Version, mig.Name)
		}

		err := m.rollbackMigration(ctx, mig)
		if err != nil {
			return fmt.Errorf("failed to rollback migration %s: %w", mig.Version, err)
		}

		if !m.Quiet {
			fmt.Printf("      Rolled back %s\n", mig.Version)
		}
		count++
		if steps > 0 && count >= steps {
			break
		}
	}

	if count == 0 {
		if !m.Quiet {
			fmt.Println("No migrations to rollback.")
		}
	} else {
		if !m.Quiet {
			fmt.Printf("Successfully rolled back %d migrations.\n", count)
		}
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

func (m *Migrator) rollbackMigration(ctx context.Context, mig Migration) error {
	content, err := m.readFile(mig.DownFile)
	if err != nil {
		return err
	}

	return m.DB.ExecTx(ctx, func(tx database.Tx) error {
		if err := tx.Exec(ctx, "UPDATE schema_migrations SET dirty = TRUE WHERE version = $1", mig.Version); err != nil {
			return err
		}
		if err := tx.Exec(ctx, string(content)); err != nil {
			return err
		}
		return tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", mig.Version)
	})
}

func (m *Migrator) applyMigration(ctx context.Context, mig Migration) error {
	content, err := m.readFile(mig.UpFile)
	if err != nil {
		return err
	}

	// 1. Mark as dirty BEFORE running the migration script.
	// We do this in a separate call to ensure it persists even if the main script fails.
	err = m.DB.Exec(
		ctx,
		"INSERT INTO schema_migrations (version, dirty) VALUES ($1, TRUE) ON CONFLICT (version) DO UPDATE SET dirty = TRUE",
		mig.Version,
	)
	if err != nil {
		return fmt.Errorf("failed to mark migration as dirty: %w", err)
	}

	// 2. Run the migration script in a transaction.
	if err = m.DB.ExecTx(ctx, func(tx database.Tx) error {
		return tx.Exec(ctx, string(content))
	}); err != nil {
		return err
	}

	// 3. Mark as clean AFTER successful migration.
	if err = m.DB.Exec(ctx, "UPDATE schema_migrations SET dirty = FALSE WHERE version = $1", mig.Version); err != nil {
		return fmt.Errorf("failed to mark migration as clean: %w", err)
	}

	return nil
}

// envRefPattern matches only the braced ${VAR} form. Bare $VAR is deliberately
// not supported so that SQL's own dollar syntax survives untouched: $$ (PL/pgSQL
// dollar quoting) and $1 (bind placeholders) must pass through verbatim.
var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// commentSpans returns the byte ranges of SQL comments in content: -- to
// end-of-line and /* */ (nestable, per the SQL standard). Ranges inside single
// quoted strings are not comments, so quotes are tracked too — otherwise a
// literal like '--' would blind the scanner to the rest of the file.
func commentSpans(content []byte) [][2]int {
	var spans [][2]int
	for i := 0; i < len(content); {
		switch {
		case content[i] == '\'':
			// Skip the string literal, honouring '' as an escaped quote.
			i++
			for i < len(content) {
				if content[i] == '\'' {
					if i+1 < len(content) && content[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case content[i] == '-' && i+1 < len(content) && content[i+1] == '-':
			start := i
			for i < len(content) && content[i] != '\n' {
				i++
			}
			spans = append(spans, [2]int{start, i})
		case content[i] == '/' && i+1 < len(content) && content[i+1] == '*':
			start, depth := i, 0
			for i < len(content) {
				if content[i] == '/' && i+1 < len(content) && content[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if content[i] == '*' && i+1 < len(content) && content[i+1] == '/' {
					depth--
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				i++
			}
			spans = append(spans, [2]int{start, i})
		default:
			i++
		}
	}
	return spans
}

// expandEnv substitutes ${VAR} references with their environment values.
// A reference to an unset variable is an error rather than an empty string:
// silently expanding to "" would happily create a role with a blank password.
//
// References inside SQL comments are left alone, so a migration can document
// the ${VAR} syntax in a comment without that mention being resolved as a real
// reference (and failing the whole migration when no such variable is set).
func expandEnv(content []byte, name string) ([]byte, error) {
	comments := commentSpans(content)
	inComment := func(pos int) bool {
		for _, s := range comments {
			if pos >= s[0] && pos < s[1] {
				return true
			}
		}
		return false
	}

	var (
		missing []string
		out     []byte
		last    int
	)
	for _, loc := range envRefPattern.FindAllSubmatchIndex(content, -1) {
		if inComment(loc[0]) {
			continue
		}
		key := string(content[loc[2]:loc[3]])
		val, ok := os.LookupEnv(key)
		if !ok {
			missing = append(missing, key)
			continue
		}
		out = append(out, content[last:loc[0]]...)
		out = append(out, val...)
		last = loc[1]
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("migration %s references unset environment variable(s): %s", name, strings.Join(missing, ", "))
	}
	return append(out, content[last:]...), nil
}

// readFile reads a migration file from fsys (if set) or from disk, expanding
// any ${VAR} environment references in the contents.
func (m *Migrator) readFile(name string) ([]byte, error) {
	var (
		content []byte
		err     error
	)
	if m.fsys != nil {
		content, err = fs.ReadFile(m.fsys, m.Dir+"/"+name)
	} else {
		content, err = os.ReadFile(filepath.Join(m.Dir, name))
	}
	if err != nil {
		return nil, err
	}
	return expandEnv(content, name)
}

func (m *Migrator) Create(name string) error {
	// Create only works with OS filesystem, not embed.FS.
	if m.fsys != nil {
		return fmt.Errorf("cannot create migration files on an embed.FS")
	}

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
	err := m.DB.QueryRow(ctx, "SELECT version FROM schema_migrations WHERE dirty = TRUE").Scan(&version)
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

	content, err := m.readFile(targetMig.DownFile)
	if err != nil {
		return err
	}

	err = m.DB.ExecTx(ctx, func(tx database.Tx) error {
		return tx.Exec(ctx, string(content))
	})
	if err != nil {
		return fmt.Errorf("failed to execute rollback for repair: %w", err)
	}

	if err = m.DB.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", version); err != nil {
		return fmt.Errorf("failed to clear dirty record after repair: %w", err)
	}

	fmt.Printf("Successfully repaired and rolled back to version before %s.\n", version)
	return nil
}
