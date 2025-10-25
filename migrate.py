#!/usr/bin/env python3
"""
Database Migration Tool - Phase 1: Basic Setup
"""

import argparse
from pathlib import Path
import sys
import psycopg2
from psycopg2.extras import RealDictCursor
from psycopg2.extensions import ISOLATION_LEVEL_AUTOCOMMIT
from dotenv import load_dotenv
from os import getenv
from datetime import datetime

class MigrationTool:
    def __init__(self, migrations_dir="migrations", seeds_dir="seeds"):
        """
        Initialize the migration tool with paths
        
        Args:
            migrations_dir: Directory to store migration files
            seeds_dir: Directory to store seed files
        """
        # PostgreSQL connection parameters
        # These can be customized or read from environment variables
        load_dotenv()
        self.db_config = {
            'host': getenv('DB_HOST'),
            'port': int(getenv('DB_PORT')),
            'user': getenv('DB_USERNAME'),
            'password': getenv('DB_PASSWORD')
        }
        
        required_vars = ['DB_HOST', 'DB_PORT', 'DB_USERNAME', 'DB_PASSWORD']
        missing = [v for v in required_vars if not getenv(v)]
        if missing:
            raise EnvironmentError(f"✗ Missing environment variables: {', '.join(missing)}")
        
        # Path() converts string to a Path object for better file handling
        self.migrations_dir = Path(migrations_dir)
        self.seeds_dir = Path(seeds_dir)
        self.conn = None  # Database connection (starts as None)

    def connect(self):
        """
        Create a connection to the PostgreSQL database
        
        Returns:
            psycopg2.Connection object
        """
        self.conn = psycopg2.connect(
            **self.db_config,
            cursor_factory=RealDictCursor
        )
    
        self.conn.autocommit = False
        return self.conn
    
    def close(self):
        """Close the database connection if it's open"""
        if self.conn:
            self.conn.close()
            self.conn = None
            
    
    def database_exists(self, db_name):
        """
        Check if a database exists
        
        Args:
            db_name: Name of the database to check
            
        Returns:
            bool: True if database exists, False otherwise
        """
        try:
            # connect to postgres instead of target DB
            conn = psycopg2.connect(
                host=self.db_config['host'],
                port=self.db_config['port'],
                user=self.db_config['user'],
                password=self.db_config['password'],
                database='postgres'
            )
            conn.set_isolation_level(ISOLATION_LEVEL_AUTOCOMMIT)
            cursor = conn.cursor()
            
            # Check if our database exists
            cursor.execute(
                "SELECT 1 FROM pg_database WHERE datname = %s",
                (db_name,)
            )
            exists = cursor.fetchone() is not None
            
            cursor.close()
            self.close()
            return exists
            
        except psycopg2.Error as e:
            print(f"  ✗ Error checking database: {e}")
            self.close()
            return False
    
    def create_database(self):
        """Create the database if it doesn't exist"""
        db_name = self.db_config['database']
        if self.database_exists(db_name):
            print(f"  ✓ Database '{db_name}' already exists")
            return
        
        try:
            # Connect to postgres database to create new database
            conn = psycopg2.connect(
                host=self.db_config['host'],
                port=self.db_config['port'],
                user=self.db_config['user'],
                password=self.db_config['password'],
                database='postgres'
            )
            
            # Set autocommit for CREATE DATABASE (required)
            conn.set_isolation_level(ISOLATION_LEVEL_AUTOCOMMIT)
            cursor = conn.cursor()
            
            # Create the database
            cursor.execute(f'CREATE DATABASE {db_name}')
            print(f"  ✓ Created database: {db_name}")
            
            self.close()
            
        except psycopg2.Error as e:
            print(f"  ✗ Error creating database: {e}")
            self.close()
            raise
        
    def create_migration(self, name: str):
        """
        Create a new migration file with timestamp.
        """
        self.migrations_dir.mkdir(exist_ok=True)
        db_migrations_dir = Path(f"{self.migrations_dir}/{self.db_config['database']}")
        db_migrations_dir.mkdir(exist_ok=True)
        
        timestamp = datetime.now().strftime("%Y%m%d%H%M%S")
        safe_name = name.lower().replace(" ", "_")
        filename_up = f"{timestamp}_{safe_name}.up.sql"
        filename_down = f"{timestamp}_{safe_name}.down.sql"

        path_up = db_migrations_dir/filename_up
        path_down = db_migrations_dir/filename_down
        
        template_up = f"""-- Up Migration {name} 
-- Created At {datetime.now().isoformat()}
        
-- TODO: Write you up migration here

"""
        template_down = f"""-- Down Migration {name} 
-- Created At {datetime.now().isoformat()}
        
-- TODO: Write you down migration here

"""
        
        path_up.write_text(template_up)
        path_down.write_text(template_down)
        print(f"✓ Created up migration file: {path_up}")
        print(f"✓ Created down migration file: {path_down}")
        
    # Future impl
    # def create_seed(self, name: str):
        # """
        # Create a new seed file with timestamp.
        # """
        # self.seeds_dir.mkdir(exist_ok=True)
        
        # timestamp = datetime.now().strftime("%Y%m%d%H%M%S")
        # safe_name = name.lower().replace(" ", "_")
        # filename_up = f"{timestamp}_{safe_name}.up.sql"
        # filename_down = f"{timestamp}_{safe_name}.down.sql"

        # path_up = self.seeds_dir/filename_up
        # path_down = self.seeds_dir/filename_down
        
        # template_up = f"""-- Up Migration {name} 
        # -- Created At {datetime.now().isoformat()}
        
        # -- TODO: Write you up seed here
        
        # """
        # template_down = f"""-- Up Migration {name} 
        # -- Created At {datetime.now().isoformat()}
        
        # -- TODO: Write you up seed here
        
        # """
        
        # path_up.write_text(template_up)
        # path_down.write_text(template_down)
        # print(f"✓ Created up seed file: {path_up}")
        # print(f"✓ Created down seed file: {path_down}")
        
    def migrate(self, direction="up", step=None):
        """
        Executes migration files.
        """
        
        self.migrations_dir.mkdir(exist_ok=True)
        db_migrations_dir = Path(f"{self.migrations_dir}/{self.db_config['database']}")
        db_migrations_dir.mkdir(exist_ok=True)
        
        try:
            conn = self.connect()
            cursor = conn.cursor()
            
            cursor.execute("""
            SELECT version, dirty FROM migrations LIMIT 1               
            """)
            
            row = cursor.fetchone()
            current_version = row['version'] if row else None
            dirty = row['dirty'] if row else False

            if dirty:
                print("✗ Dirty state detected. Please fix or reset manually.")
                return

            # List all migrations
            migrations = sorted(db_migrations_dir.glob("*.up.sql"))
            migration_versions = [m.stem.split("_")[0] for m in migrations]
            to_run = [m for m in migrations if current_version is None or m.stem.split("_")[0] > current_version]
            
            if direction == "down":
                migrations = sorted(db_migrations_dir.glob("*.down.sql"), reverse=True)
                to_run = [m for m in migrations if current_version is not None and m.stem.split("_")[0] <= current_version]
                
            if step:
                to_run = to_run[:step]

            if not to_run:
                print("✓ No migrations to apply.")
                self.close()
                return

            try:
                for m in to_run:
                    version = m.stem.split("_")[0]
                    print(f"→ Running {direction} migration: {m.name}")
                    cursor.execute("UPDATE migrations SET dirty = TRUE WHERE TRUE")
                    self.conn.commit()

                    sql = m.read_text()
                    cursor.execute(sql)

                    if direction == "up":
                        cursor.execute("DELETE FROM migrations")
                        cursor.execute("INSERT INTO migrations (version, dirty) VALUES (%s, FALSE)", (version,))
                    else:
                        # For down, revert to previous version if exists
                        idx = migration_versions.index(version)
                        prev = migration_versions[idx - 1] if idx > 0 else None
                        cursor.execute("DELETE FROM migrations")
                        if prev:
                            cursor.execute("INSERT INTO migrations (version, dirty) VALUES (%s, FALSE)", (prev,))
                    self.conn.commit()
                    print(f"  ✓ Applied {m.name}")
            except Exception as e:
                self.conn.rollback()
                cursor.execute("UPDATE migrations SET dirty = TRUE WHERE TRUE")
                self.conn.commit()
                print(f"  ✗ Migration failed: {e}")
                raise
            finally:
                cursor.close()
                self.close()
                
        except psycopg2.Error as e:
            print(f"  ✗ Database error: {e}")
            if self.conn:
                self.conn.rollback()
            raise
           
    def init(self, dbName):
        """
        Initialize the migration system:
        1. Create the database if it doesn't exist
        2. Create directories for migrations and seeds
        3. Create a table in the database to track migrations
        """
        print("Initializing migration system...")
        
        self.db_config['database'] = dbName
        
        # Step 1: Create database if needed
        self.create_database()
        
        # Step 2: Create directories
        # exist_ok=True means "don't error if directory already exists"
        self.migrations_dir.mkdir(exist_ok=True)
        print(f"  ✓ Created directory: {self.migrations_dir}/{dbName}")
        
        self.seeds_dir.mkdir(exist_ok=True)
        print(f"  ✓ Created directory: {self.seeds_dir}/{dbName}")
        
        # Step 3: Connect to database
        try:
            conn = self.connect()
            cursor = conn.cursor()
            
            # Step 3: Create migrations tracking table
            # This table stores which migrations have been applied
            cursor.execute("""
                CREATE TABLE IF NOT EXISTS migrations (
                    version VARCHAR(255) PRIMARY KEY,
                    dirty BOOLEAN DEFAULT FALSE
                )
            """)
            
            # Commit saves the changes to the database
            conn.commit()
            print(f"  ✓ Created migrations tracking table")
            
        except psycopg2.Error as e:
            print(f"  ✗ Database error: {e}")
            if self.conn:
                self.conn.rollback()
            raise
        finally:
            # Step 4: Close connection
            cursor.close()
            self.close()
        
        print(f"\n✓ Migration system initialized!")
        print(f"  Database: {self.db_config['database']} @ {self.db_config['host']}:{self.db_config['port']}")
        print(f"  Migrations: {self.migrations_dir}/")
        print(f"  Seeds: {self.seeds_dir}/")
            
    def test_command(self):
        """Test command to verify CLI is working"""
        print("✓ CLI is working!")
        print(f"  Database path: {self.db_config['database']}")
        print(f"  Database port: {self.db_config['port']}")

def main():
    # Create the main parser
    parser = argparse.ArgumentParser(
        description="Database Migration Tool",
        formatter_class=argparse.RawDescriptionHelpFormatter
    )
    
    # Add subcommands
    subparsers = parser.add_subparsers(dest='command', help='Available commands')
    
    # Test command (just to verify it works)
    subparsers.add_parser('init', help='Initialize the migration system')
    subparsers.add_parser('test', help='Test that CLI is working')
    make_migration_parser = subparsers.add_parser('make:migration', help='Create migration files up & down with timestamp as versioning')
    migrate_parser = subparsers.add_parser('migrate', help='Execute migration files')
    
    make_migration_parser.add_argument(
        'name',
        type=str,
        help='Name of the migration (e.g. create_users_table)'
    )
    
    migrate_parser.add_argument(
        'direction',
        type=str,
        choices=['up', 'down'],
        help='Migration direction: up or down'
    )
    
    migrate_parser.add_argument(
        'db',
        type=str,
        choices=['clefinport_user', 'clefinport_wallet', 'clefinport_log'],
        help='Migration direction: up or down'
    )
    
    migrate_parser.add_argument(
        '--step',
        type=int,
        default=None,
        help='Number of migration steps to run (default: all pending/applied)'
    )
    
    # Parse arguments
    args = parser.parse_args()
    
    # Create tool instance
    tool = MigrationTool()
    
    # Route to appropriate command
    try:
        if args.command == 'init':
            tool.init(args.db)
        elif args.command == 'test':
            tool.test_command()
        elif args.command == 'make:migration':
            tool.create_migration(args.name)
        elif args.command == 'migrate':
            tool.init(args.db)
            tool.migrate(args.direction, args.step)
        else:
            parser.print_help()
    except Exception as e:
        print(f"✗ Error: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()