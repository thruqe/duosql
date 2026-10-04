package duosql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// QueryExecutor defines common read operations over database connections and transactions.
type QueryExecutor interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Dialect() Dialect
	notifyHook(ctx context.Context, query string, args []any, duration time.Duration, err error)
}

// ExecExecutor extends QueryExecutor with write execution commands.
type ExecExecutor interface {
	QueryExecutor
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// QueryHook allows intercepting executed SQL statements for telemetry, auditing, and logging.
type QueryHook func(ctx context.Context, query string, args []any, duration time.Duration, err error)

// Option configures database connection parameters.
type Option func(*DB)

// WithMaxOpenConns sets maximum open connection pool limit.
func WithMaxOpenConns(n int) Option {
	return func(db *DB) {
		db.sqlDB.SetMaxOpenConns(n)
	}
}

// WithMaxIdleConns sets maximum idle connections in the pool.
func WithMaxIdleConns(n int) Option {
	return func(db *DB) {
		db.sqlDB.SetMaxIdleConns(n)
	}
}

// WithConnMaxLifetime sets maximum connection lifetime.
func WithConnMaxLifetime(d time.Duration) Option {
	return func(db *DB) {
		db.sqlDB.SetConnMaxLifetime(d)
	}
}

// WithConnMaxIdleTime sets maximum connection idle duration.
func WithConnMaxIdleTime(d time.Duration) Option {
	return func(db *DB) {
		db.sqlDB.SetConnMaxIdleTime(d)
	}
}

// WithHook attaches a query inspection hook.
func WithHook(hook QueryHook) Option {
	return func(db *DB) {
		if hook != nil {
			db.hooks = append(db.hooks, hook)
		}
	}
}

// DB manages connection pooling, dialect resolution, and transaction lifecycles.
type DB struct {
	sqlDB   *sql.DB
	dialect Dialect
	hooks   []QueryHook
}

// Open initializes a database session for the specified dialect and DSN.
func Open(kind DialectKind, dsn string, opts ...Option) (*DB, error) {
	var driverName string
	var dialect Dialect

	switch kind {
	case DialectPostgres:
		driverName = "pgx"
		dialect = NewPostgresDialect()
	case DialectSQLite:
		driverName = "sqlite"
		dialect = NewSQLiteDialect()
	default:
		return nil, fmt.Errorf("duosql: unsupported dialect kind %q", kind)
	}

	rawDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("duosql: open driver %s: %w", driverName, err)
	}

	return Wrap(rawDB, dialect, opts...), nil
}

// Wrap adopts an existing *sql.DB instance with the designated dialect.
func Wrap(rawDB *sql.DB, dialect Dialect, opts ...Option) *DB {
	db := &DB{
		sqlDB:   rawDB,
		dialect: dialect,
	}
	for _, opt := range opts {
		opt(db)
	}
	return db
}

// SQLDB returns the underlying *sql.DB handle.
func (db *DB) SQLDB() *sql.DB {
	return db.sqlDB
}

// Dialect returns the database engine compiler.
func (db *DB) Dialect() Dialect {
	return db.dialect
}

// Close closes the underlying database pool.
func (db *DB) Close() error {
	return db.sqlDB.Close()
}

// Ping verifies connectivity to the database server.
func (db *DB) Ping(ctx context.Context) error {
	return db.sqlDB.PingContext(ctx)
}

// Schema returns a DDL schema builder for table management.
func (db *DB) Schema() *SchemaBuilder {
	return NewSchemaBuilder(db, db.dialect)
}

// ExecContext executes a write command and invokes configured telemetry hooks.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := db.sqlDB.ExecContext(ctx, query, args...)
	db.notifyHook(ctx, query, args, time.Since(start), err)
	return res, err
}

// QueryContext executes a read query and invokes configured telemetry hooks.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := db.sqlDB.QueryContext(ctx, query, args...)
	db.notifyHook(ctx, query, args, time.Since(start), err)
	return rows, err
}

// QueryRowContext executes a single-row query.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.sqlDB.QueryRowContext(ctx, query, args...)
}

func (db *DB) notifyHook(ctx context.Context, query string, args []any, duration time.Duration, err error) {
	for _, hook := range db.hooks {
		hook(ctx, query, args, duration, err)
	}
}

// Begin begins a new atomic transaction.
func (db *DB) Begin(ctx context.Context) (*Tx, error) {
	sqlTx, err := db.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("duosql: begin tx: %w", err)
	}
	return &Tx{
		sqlTx:   sqlTx,
		dialect: db.dialect,
		hooks:   db.hooks,
	}, nil
}

// Transaction executes a transactional function, committing on nil return or
// rolling back automatically if an error or panic occurs.
func (db *DB) Transaction(ctx context.Context, fn func(tx *Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(tx)
	return err
}

// Tx wraps an active transaction session.
type Tx struct {
	sqlTx      *sql.Tx
	dialect    Dialect
	hooks      []QueryHook
	spSequence atomic.Uint64
}

// Dialect returns the database engine compiler.
func (tx *Tx) Dialect() Dialect {
	return tx.dialect
}

// Schema returns a DDL schema builder bound to this transaction.
func (tx *Tx) Schema() *SchemaBuilder {
	return NewSchemaBuilder(tx, tx.dialect)
}

// Commit persists changes made in this transaction.
func (tx *Tx) Commit() error {
	return tx.sqlTx.Commit()
}

// Rollback cancels changes made in this transaction.
func (tx *Tx) Rollback() error {
	return tx.sqlTx.Rollback()
}

// ExecContext executes a write query inside the transaction.
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := tx.sqlTx.ExecContext(ctx, query, args...)
	tx.notifyHook(ctx, query, args, time.Since(start), err)
	return res, err
}

// QueryContext executes a read query inside the transaction.
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := tx.sqlTx.QueryContext(ctx, query, args...)
	tx.notifyHook(ctx, query, args, time.Since(start), err)
	return rows, err
}

// QueryRowContext executes a single-row query inside the transaction.
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.sqlTx.QueryRowContext(ctx, query, args...)
}

func (tx *Tx) notifyHook(ctx context.Context, query string, args []any, duration time.Duration, err error) {
	for _, hook := range tx.hooks {
		hook(ctx, query, args, duration, err)
	}
}

// Savepoint establishes a transactional savepoint for nested rollbacks.
func (tx *Tx) Savepoint(name string) error {
	if name == "" {
		seq := tx.spSequence.Add(1)
		name = fmt.Sprintf("sp_%d", seq)
	}
	_, err := tx.sqlTx.Exec(fmt.Sprintf("SAVEPOINT %s", tx.dialect.QuoteIdentifier(name)))
	if err != nil {
		return fmt.Errorf("duosql: create savepoint %s: %w", name, err)
	}
	return nil
}

// RollbackTo rolls back changes made since the specified savepoint.
func (tx *Tx) RollbackTo(name string) error {
	if name == "" {
		return errors.New("duosql: savepoint name cannot be empty")
	}
	_, err := tx.sqlTx.Exec(fmt.Sprintf("ROLLBACK TO SAVEPOINT %s", tx.dialect.QuoteIdentifier(name)))
	if err != nil {
		return fmt.Errorf("duosql: rollback to savepoint %s: %w", name, err)
	}
	return nil
}

// ReleaseSavepoint frees resources allocated to the named savepoint.
func (tx *Tx) ReleaseSavepoint(name string) error {
	if name == "" {
		return errors.New("duosql: savepoint name cannot be empty")
	}
	_, err := tx.sqlTx.Exec(fmt.Sprintf("RELEASE SAVEPOINT %s", tx.dialect.QuoteIdentifier(name)))
	if err != nil {
		return fmt.Errorf("duosql: release savepoint %s: %w", name, err)
	}
	return nil
}
