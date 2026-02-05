# mig (Go Migration Tool)

**A simple, robust, and platform-agnostic migration tool for PostgreSQL.**  
Works great for local development, CI/CD, and Docker environments.

---

## Quick Start (The "I just want to use it" Guide)

Follow this flow to install and run your first migration in 2 minutes.

### 1. Installation

**Option 1: Using Make (Recommended)**

```bash
git clone github.com/mystaline/migration-tool
cd migration-tool
make install
```

**Option 2: Manual Installation**

```bash
git clone github.com/mystaline/migration-tool
cd migration-tool
go build -o mig ./cmd/main.go
sudo mv mig /usr/local/bin/
```

### 2. Configure Your Project

You don't need a config file. Just tell `mig` where your files are and how to connect to the DB.

The easiest way is to **export variables** in your terminal so you don't have to type them every time:

```bash
# RECOMENDED: Run this in your terminal before starting
export MIGRATIONS_DIR=./migrations   # Or wherever your .sql files are
export DB_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable"
```

### 3. Workflow

**Step A: Create a Migration**  
This creates a pair of `.up.sql` and `.down.sql` files.

```bash
mig create add_users_table
```

**Step B: Write SQL**

- Edit `..._add_users_table.up.sql`: Write your `CREATE TABLE` statement.
- Edit `..._add_users_table.down.sql`: Write your `DROP TABLE` statement.

**Step C: Ensure Integrity (Recommended)**  
Ensure your Up and Down logic is perfect by running the integrity test.

```bash
mig test
```

**Step D: Apply Changes**  
Run the pending migrations against your DB.

```bash
mig up
```

---

## Command Reference

| Command             | Description                                                   |
| :------------------ | :------------------------------------------------------------ |
| `mig init`          | Initialize the `schema_migrations` tracking table in your DB. |
| `mig create <name>` | Generate timestamped .up.sql and .down.sql files.             |
| `mig up`            | Apply all pending migrations.                                 |
| `mig down`          | Rollback the _last_ migration only.                           |
| `mig status`        | Check which migrations are applied vs pending.                |
| `mig test`          | Run a safety check (Up -> Down -> Up) on a temporary DB.      |

---

## Configuration & CLI Flags

You can configure `mig` in 3 ways (in order of precedence):

1.  **CLI Flags**: Pass arguments directly to the command.

### i. Database Connection (`--db-url`)

**Required** for `up`, `down`, `status`, `test`, `init`.

- **Flag**: `--db-url "postgres://..."`
- **Env Var**: `DB_URL`

### ii. Migrations Directory (`--dir`)

**Optional**. Defaults to `./migrations` if not set.

- **Flag**: `--dir ./path/to/files`
- **Env Var**: `MIGRATIONS_DIR`

For example:

```bash
mig create add_balance_column --dir pkg/migrations/wallet

mig up --dir pkg/migrations/wallet --db-url "postgres://admin:secret@localhost:5432/wallet_db"
```

2.  **Environment Variables**: `export VAR=...` in your shell.

Export variables in your shell before running the command. For example:

```bash
export DB_URL="postgres://admin:secret@localhost:5432/wallet_db"
export MIGRATIONS_DIR=pkg/migrations/wallet
```

3.  **`.env` File**: A file named `.env` in the current directory.

If you don't have a `.env` file, you can create one in the root of your project then define the variables in it. Variables name should be in uppercase and separated by underscores. For example:

```bash
DB_URL="postgres://admin:secret@localhost:5432/wallet_db"
MIGRATIONS_DIR=pkg/migrations/wallet
```

---

## Under the Hood (Schema)

The tool tracks migrations in a simple table called `schema_migrations`.

| Column       | Type      | Description                                                           |
| :----------- | :-------- | :-------------------------------------------------------------------- |
| `version`    | VARCHAR   | Unique ID (Timestamp) of the migration. (e.g., `20260131120000`)      |
| `dirty`      | BOOLEAN   | **Safety Lock**. `true` while a migration is running or if it failed. |
| `applied_at` | TIMESTAMP | When the migration successfully completed.                            |

### How "Dirty" State Works

1.  **Start**: Tool creates a row with `version=...` and `dirty=true`.
2.  **Execute**: Tool runs your `.up.sql` script within a transaction.
3.  **Success**: Tool updates the row to `dirty=false`.
4.  **Failure**: Tool exits. The row remains `dirty=true`.
    - _Note: Because of Transactional DDL, your SQL changes are rolled back, but this dirty record remains to force you to review the error._

---

## Troubleshooting (Manual Repair)

If a migration fails (e.g., syntax error in SQL), `mig status` will show it as **Pending** or **Dirty**, and `mig up` will refuse to run until you fix it.

**Step-by-Step Fix:**

1.  **Identify the Error**: Read the error log from the failed `mig up` command.
2.  **Fix Code**: Open your `...up.sql` file and correct the SQL syntax.
3.  **Unlock (Clean Dirty State)**:
    Since the SQL transaction rolled back, your data is safe, but the "lock" is still on. You must manually delete the dirty record:

    ```bash
    # Connect to your DB
    psql "$DB_URL"

    # Run this query (replace VERSION with your failed timestamp)
    DELETE FROM schema_migrations WHERE version = '20260131172602';
    ```

4.  **Retry**:
    ```bash
    mig up
    ```

---

## Docker Support

You don't need Go installed to run this tool. You can use the Docker image to run commands against your database.

### Why use Volumes? (`-v`)

The migration tool runs inside a container, but your SQL files live on your computer.
We use a **volume** (`-v $(pwd)/migrations:/migrations`) to give the container access to your local folder.
This way, the tool can read your latest SQL files without you needing to rebuild the Docker image every time you make a change.

### 1. Using `docker run` (Ad-hoc)

You can run any `mig` command by appending it to the end of the docker line.

**Run `mig status`:**

```bash
docker run --rm \
  -e DB_URL="postgres://user:pass@host.docker.internal:5432/mydb" \
  -v $(pwd)/migrations:/migrations \
  mystaline/migration-tool status
```

**Run `mig up`:**

```bash
docker run --rm \
  -e DB_URL="postgres://user:pass@host.docker.internal:5432/mydb" \
  -v $(pwd)/migrations:/migrations \
  mystaline/migration-tool up
```

_> Note: `host.docker.internal` allows the container to connect to a Postgres database running on your local machine outside of Docker._

### 2. Using Docker Compose (Service)

Add the migrator to your stack to automatically run migrations or simplify commands.

```yaml
services:
  migrator:
    image: mystaline/migration-tool:latest
    environment:
      - DB_URL=postgres://user:pass@db:5432/mydb
      # Tell the tool where to find the mounted files
      - MIGRATIONS_DIR=/my-project/migrations
    volumes:
      # Map local folder -> container folder
      - ./my-project/migrations:/migrations
    depends_on:
      db:
        condition: service_healthy
    # Optional: Automatically run 'up' when the container starts
    command: up
```

**Running commands via Compose:**

Once defined in your compose file, you can run commands easily:

```bash
# Check status
docker compose run --rm migrator status

# Rollback one step
docker compose run --rm migrator down
```

---

## FAQ

**Q: Where should I put my migration files?**  
A: Anywhere you want! Just point the tool to that folder using `--dir` or `MIGRATIONS_DIR`. We recommend keeping them nicely organized in your project repo, e.g., `internal/db/migrations`.

**Q: What if a migration fails?**  
A: `mig` takes a safety-first approach:

1.  **Transactional Rollback**: Your SQL changes are automatically rolled back, so your database schema stays clean.
2.  **Dirty State**: The version is marked as **dirty** in the `schema_migrations` table to lock the system.
3.  **Manual Fix**: You must fix your `.sql` file and then manually remove the dirty record from `schema_migrations`.

**Q: Why do I need `.down.sql` files?**  
A: They allow you to undo changes safely (`mig down`). But crucially, `mig test` uses them to prove your migration is reversible and safe before you even deploy it.
