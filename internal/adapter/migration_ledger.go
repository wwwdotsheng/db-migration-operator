package adapter

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	dbv1 "github.com/wwwdotsheng/db-migration-operator/api/v1"
)

var ErrMigrationLockBusy = errors.New("target database migration lock is busy")

type MigrationRecord struct {
	Version  int
	Status   dbv1.SchemaMigrationStatusPhase
	Checksum string
}

// MigrationLedger uses the same database session that owns the migration lock.
type MigrationLedger interface {
	Records(context.Context) ([]MigrationRecord, error)
	Start(context.Context, string, int, string) error
	ExecSQL(context.Context, []string) (int, error)
	MarkDone(context.Context, int, string) error
}

type migrationLedger struct {
	conn     *sql.Conn
	postgres bool
}

func withMigrationLock(ctx context.Context, db *sql.DB, acquire, release string, postgres bool, run func(MigrationLedger) error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration session: %w", err)
	}
	defer conn.Close()

	// A query that times out may still have obtained the lock on the server.
	// Discard that session instead of returning it to the connection pool.
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var acquired bool
	if err := conn.QueryRowContext(ctx, acquire).Scan(&acquired); err != nil {
		discard()
		return fmt.Errorf("acquire target database lock: %w", err)
	}
	if !acquired {
		return ErrMigrationLockBusy
	}

	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var released bool
		unlockErr := conn.QueryRowContext(unlockCtx, release).Scan(&released)
		if unlockErr != nil || !released {
			discard()
			if err == nil {
				err = fmt.Errorf("release target database lock: released=%t, error=%v", released, unlockErr)
			}
		}
	}()

	ledger := &migrationLedger{conn: conn, postgres: postgres}
	if err := ledger.init(ctx); err != nil {
		return fmt.Errorf("initialize migration ledger: %w", err)
	}
	return run(ledger)
}

func (l *migrationLedger) init(ctx context.Context) error {
	id := "INT AUTO_INCREMENT"
	if l.postgres {
		id = "SERIAL"
	}
	_, err := l.conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations ("+
		"id "+id+" PRIMARY KEY, name VARCHAR(50) NOT NULL, version INT NOT NULL UNIQUE, "+
		"status VARCHAR(30) NOT NULL, checksum VARCHAR(64) NULL)")
	if err != nil {
		return err
	}

	// Existing installations have the old table without checksum. Legacy rows
	// retain NULL so the operator never guesses which SQL they represent.
	if l.postgres {
		var exists bool
		err = l.conn.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass('schema_migrations')
			AND attname = 'checksum' AND NOT attisdropped
		)`).Scan(&exists)
		if err != nil || exists {
			return err
		}
		_, err = l.conn.ExecContext(ctx, "ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum VARCHAR(64) NULL")
		return err
	}
	var exists bool
	err = l.conn.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = 'schema_migrations' AND column_name = 'checksum'
	)`).Scan(&exists)
	if err != nil || exists {
		return err
	}
	_, err = l.conn.ExecContext(ctx, "ALTER TABLE schema_migrations ADD COLUMN checksum VARCHAR(64) NULL")
	return err
}

func (l *migrationLedger) bind(query string) string {
	if !l.postgres {
		return query
	}
	var b strings.Builder
	arg := 0
	for _, c := range query {
		if c == '?' {
			arg++
			fmt.Fprintf(&b, "$%d", arg)
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (l *migrationLedger) Records(ctx context.Context) ([]MigrationRecord, error) {
	rows, err := l.conn.QueryContext(ctx, "SELECT version, status, COALESCE(checksum, '') FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []MigrationRecord
	for rows.Next() {
		var record MigrationRecord
		if err := rows.Scan(&record.Version, &record.Status, &record.Checksum); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (l *migrationLedger) Start(ctx context.Context, name string, version int, checksum string) error {
	_, err := l.conn.ExecContext(ctx, l.bind("INSERT INTO schema_migrations (name, version, status, checksum) VALUES (?, ?, ?, ?)"),
		name, version, dbv1.PhaseApplying, checksum)
	return err
}

func (l *migrationLedger) ExecSQL(ctx context.Context, statements []string) (int, error) {
	for i, statement := range statements {
		if _, err := l.conn.ExecContext(ctx, statement); err != nil {
			return i, fmt.Errorf("SQL statement %d: %w", i+1, err)
		}
	}
	return len(statements), nil
}

func (l *migrationLedger) MarkDone(ctx context.Context, version int, checksum string) error {
	result, err := l.conn.ExecContext(ctx, l.bind("UPDATE schema_migrations SET status = ? WHERE version = ? AND status = ? AND checksum = ?"),
		dbv1.PhaseDone, version, dbv1.PhaseApplying, checksum)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: version %d no longer matches its Applying record", ErrVersionConflict, version)
	}
	return nil
}
