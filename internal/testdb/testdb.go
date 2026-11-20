// Package testdb creates throwaway MySQL databases for integration tests.
//
// Integration tests run only when TEST_DATABASE_DSN is set, for example:
//
//	TEST_DATABASE_DSN='root:@tcp(127.0.0.1:3306)/warehouse_test' go test ./...
//
// The database name in the DSN must end with "_test". Each test package
// gets its own database derived from that name (warehouse_test becomes
// warehouse_service_test, warehouse_httpapi_test, ...), which is dropped and
// created fresh, migrated, and dropped again when the package finishes.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/vicitritellacarmida/warehouse-go/internal/migrate"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
	"github.com/vicitritellacarmida/warehouse-go/migrations"
)

// EnvVar is the environment variable holding the test DSN.
const EnvVar = "TEST_DATABASE_DSN"

var safeName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// DB is a migrated test database.
type DB struct {
	*sql.DB
	Name  string
	admin *sql.DB
}

// SkipReason returns a non-empty message when integration tests cannot run.
func SkipReason() string {
	if os.Getenv(EnvVar) == "" {
		return EnvVar + " is not set; skipping MySQL integration tests " +
			"(example: " + EnvVar + "='root:@tcp(127.0.0.1:3306)/warehouse_test')"
	}
	return ""
}

// Open creates a fresh database for the calling package and applies all
// migrations. suffix distinguishes packages (for example "service").
func Open(suffix string) (*DB, error) {
	cfg, err := mysql.ParseDSN(os.Getenv(EnvVar))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvVar, err)
	}
	if !strings.HasSuffix(cfg.DBName, "_test") {
		return nil, fmt.Errorf("refusing to use database %q: the name in %s must end with _test", cfg.DBName, EnvVar)
	}
	name := strings.TrimSuffix(cfg.DBName, "_test") + "_" + suffix + "_test"
	if !safeName.MatchString(name) {
		return nil, fmt.Errorf("unsafe database name %q", name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	adminCfg := cfg.Clone()
	adminCfg.DBName = ""
	admin, err := store.Open(ctx, adminCfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS `" + name + "`",
		"CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			admin.Close()
			return nil, err
		}
	}
	cfg.DBName = name
	db, err := store.Open(ctx, cfg.FormatDSN())
	if err != nil {
		admin.Close()
		return nil, err
	}
	db.SetMaxOpenConns(50)
	if _, err := migrate.Up(ctx, db, migrations.FS, nil); err != nil {
		db.Close()
		admin.Close()
		return nil, fmt.Errorf("migrate %s: %w", name, err)
	}
	return &DB{DB: db, Name: name, admin: admin}, nil
}

// Close drops the database and closes the connections.
func (d *DB) Close() error {
	d.DB.Close()
	defer d.admin.Close()
	_, err := d.admin.Exec("DROP DATABASE IF EXISTS `" + d.Name + "`")
	return err
}
