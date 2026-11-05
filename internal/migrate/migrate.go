// Package migrate applies the embedded SQL migrations in version order and
// records each applied version in the schema_migrations table.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Migration is one SQL file named NNNN_description.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// lockName is the MySQL advisory lock that keeps two processes from
// migrating the same database at the same time.
const lockName = "warehouse_schema_migrate"

// Load reads all *.sql files from fsys and returns them sorted by version.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(prefix)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %q: name must look like 0001_name.sql", e.Name())
		}
		if other, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", other, e.Name(), v)
		}
		seen[v] = e.Name()
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: e.Name(), SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Up applies every migration that is not yet recorded in schema_migrations.
// It returns the names of the migrations it applied. MySQL commits DDL
// implicitly, so a failure in the middle of a file can leave that file
// partly applied; the version is only recorded when the whole file succeeds.
func Up(ctx context.Context, db *sql.DB, fsys fs.FS, logger *slog.Logger) ([]string, error) {
	migrations, err := Load(fsys)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", lockName).Scan(&got); err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	if got.Int64 != 1 {
		return nil, fmt.Errorf("could not acquire migration lock %q within 60s", lockName)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(?)", lockName)
	}()

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INT          NOT NULL PRIMARY KEY,
		name       VARCHAR(255) NOT NULL,
		applied_at DATETIME(6)  NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var done []string
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		for i, stmt := range SplitStatements(m.SQL) {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return done, fmt.Errorf("migration %s statement %d: %w", m.Name, i+1, err)
			}
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
			m.Version, m.Name, time.Now().UTC()); err != nil {
			return done, fmt.Errorf("record migration %s: %w", m.Name, err)
		}
		if logger != nil {
			logger.Info("applied migration", "name", m.Name)
		}
		done = append(done, m.Name)
	}
	return done, nil
}

// SplitStatements splits a SQL script into statements on semicolons that
// are outside quotes and comments. Lines starting with "--" are comments.
// This is enough for the schema files in this project; it does not support
// stored procedure bodies with BEGIN ... END blocks.
func SplitStatements(script string) []string {
	var (
		out     []string
		cur     strings.Builder
		quote   rune
		comment bool
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case comment:
			if r == '\n' {
				comment = false
				cur.WriteRune(r)
			}
			continue
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		case r == '-' && i+1 < len(runes) && runes[i+1] == '-':
			comment = true
			continue
		case r == '\'' || r == '"' || r == '`':
			quote = r
		case r == ';':
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}
