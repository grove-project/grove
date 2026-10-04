// Package sqlitefiles keeps a SQLite database in a Grove file. SQLite writes
// the local file as usual; Sync creates an application-safe point and
// publishes it, so failover and recovery resume from the last synced
// database, never from a half-written one.
//
// Grove does not know the file is a database. This package owns the SQLite
// side of the contract: checkpoint the write-ahead log into the database
// file and hold off writers while Grove snapshots it.
package sqlitefiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/grove-project/grove"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Open opens the database at file's local path in write-ahead-log mode.
func Open(file grove.File) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+file.LocalPath()+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(FULL)")
}

// Sync publishes the database's committed transactions as a new version of
// file. It checkpoints the write-ahead log into the database file, then
// takes the write lock so no transaction lands while Grove snapshots the
// file, and releases it once the snapshot is taken.
func Sync(ctx context.Context, db *sql.DB, file grove.File) (grove.Version, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return grove.Version{}, err
	}
	defer conn.Close()
	for attempt := 0; attempt < 100; attempt++ {
		var busy, frames, checkpointed int
		if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); err != nil {
			return grove.Version{}, fmt.Errorf("checkpoint: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return grove.Version{}, fmt.Errorf("hold writers: %w", err)
		}
		// A writer may have committed between the checkpoint and the lock;
		// then its frames are still in the log and the file is not yet the
		// whole database.
		info, err := os.Stat(file.LocalPath() + "-wal")
		if err == nil && info.Size() > 0 || err != nil && !errors.Is(err, os.ErrNotExist) {
			if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
				return grove.Version{}, err
			}
			continue
		}
		version, syncErr := file.Sync(ctx)
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); err != nil && syncErr == nil {
			syncErr = err
		}
		return version, syncErr
	}
	return grove.Version{}, errors.New("sqlitefiles: writers kept the write-ahead log busy")
}
