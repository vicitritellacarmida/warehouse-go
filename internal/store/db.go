// Package store is the MySQL persistence layer. It maps rows to domain
// types and exposes small, explicit queries; business decisions live in
// the domain and service packages.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// MySQL error numbers the store reacts to.
const (
	errDuplicateEntry = 1062
	errLockWait       = 1205
	errDeadlock       = 1213
	errCheckViolated  = 3819
)

// DBTX is the subset of *sql.DB and *sql.Tx used by Queries.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store owns the connection pool and runs transactions.
type Store struct {
	db         *sql.DB
	maxRetries int
}

// Queries runs statements on a connection or inside a transaction.
type Queries struct{ db DBTX }

// Open parses dsn, forces parseTime=true and loc=UTC (the store relies on
// both), opens the pool and pings the server.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_DSN: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	if _, ok := cfg.Params["time_zone"]; !ok {
		cfg.Params["time_zone"] = "'+00:00'"
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to MySQL: %w", err)
	}
	return db, nil
}

// New wraps an open pool.
func New(db *sql.DB) *Store { return &Store{db: db, maxRetries: 5} }

// DB returns the underlying pool.
func (s *Store) DB() *sql.DB { return s.db }

// Q returns Queries that run outside a transaction.
func (s *Store) Q() *Queries { return &Queries{db: s.db} }

// InTx runs fn in a READ COMMITTED transaction. Rows that need protection
// are locked explicitly with SELECT ... FOR UPDATE. If MySQL reports a
// deadlock or lock wait timeout the whole function is retried a few times
// with jittered backoff, so fn must not have side effects outside the
// transaction.
func (s *Store) InTx(ctx context.Context, fn func(q *Queries) error) error {
	for attempt := 0; ; attempt++ {
		err := s.runTx(ctx, fn)
		if err == nil || !IsRetryable(err) || attempt >= s.maxRetries {
			return err
		}
		backoff := time.Duration(attempt+1)*15*time.Millisecond + time.Duration(rand.IntN(20))*time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func (s *Store) runTx(ctx context.Context, fn func(q *Queries) error) (err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(&Queries{db: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// IsRetryable reports whether err is a deadlock or lock wait timeout.
func IsRetryable(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && (me.Number == errDeadlock || me.Number == errLockWait)
}

// IsDuplicate reports whether err is a unique key violation.
func IsDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == errDuplicateEntry
}

func isCheckViolation(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == errCheckViolated
}

// nullable helpers

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func ptrInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func nullDate(d *domain.Date) sql.NullTime {
	if d == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: d.Time(), Valid: true}
}

func ptrDate(t sql.NullTime) *domain.Date {
	if !t.Valid {
		return nil
	}
	d := domain.DateOf(t.Time)
	return &d
}

func ptrTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// inClause returns "?,?,?" and the args for an IN (...) list.
func inClause(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

func lockSuffix(forUpdate bool) string {
	if forUpdate {
		return " FOR UPDATE"
	}
	return ""
}

func rowsAffected(res sql.Result) int64 {
	n, _ := res.RowsAffected()
	return n
}
