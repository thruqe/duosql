package duosql

import (
	"fmt"
	"strings"
)

// DialectKind specifies the target relational database engine.
type DialectKind string

const (
	DialectPostgres DialectKind = "postgres"
	DialectSQLite   DialectKind = "sqlite"
)

// Dialect abstracts database-specific SQL generation, parameter binding,
// data type representations, and metadata queries between PostgreSQL and SQLite.
type Dialect interface {
	// Kind returns the dialect identifier.
	Kind() DialectKind

	// Placeholder converts an index (1-based) into the dialect's parameter token.
	// PostgreSQL returns $1, $2, etc., while SQLite returns ? for all indices.
	Placeholder(index int) string

	// QuoteIdentifier escapes identifiers such as table and column names
	// to prevent collision with reserved SQL keywords and preserve case sensitivity.
	QuoteIdentifier(name string) string

	// ColumnTypeSQL maps an internal column definition to the dialect's DDL data type string.
	ColumnTypeSQL(col ColumnDef) string

	// SupportsReturning reports whether the database natively supports RETURNING clauses.
	SupportsReturning() bool

	// HasTableSQL returns the SQL query and parameter to verify table existence.
	HasTableSQL(tableName string) (string, any)

	// FormatILike returns the SQL expression for case-insensitive pattern matching.
	FormatILike(col string, placeholder string) string

	// JSONExtractSQL compiles dialect-specific JSON path extraction expressions.
	JSONExtractSQL(col string, path string) string
}

// postgresDialect implements Dialect for PostgreSQL instances.
type postgresDialect struct{}

// NewPostgresDialect constructs a PostgreSQL dialect compiler.
func NewPostgresDialect() Dialect {
	return &postgresDialect{}
}

func (p *postgresDialect) Kind() DialectKind {
	return DialectPostgres
}

func (p *postgresDialect) Placeholder(index int) string {
	return fmt.Sprintf("$%d", index)
}

func (p *postgresDialect) QuoteIdentifier(name string) string {
	// Nested qualified identifiers like "schema.table" require quoting per segment
	// to avoid treating the period as part of the identifier token.
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		for i, part := range parts {
			parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
		}
		return strings.Join(parts, ".")
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (p *postgresDialect) ColumnTypeSQL(col ColumnDef) string {
	if col.IsAutoIncrement && col.IsPrimaryKey {
		if col.DataType == TypeBigInt {
			return "BIGSERIAL PRIMARY KEY"
		}
		return "SERIAL PRIMARY KEY"
	}

	sqlType := postgresTypeMapping(col.DataType, col.Length)
	var parts []string
	parts = append(parts, sqlType)

	if col.IsPrimaryKey && !col.IsAutoIncrement {
		parts = append(parts, "PRIMARY KEY")
	}
	if !col.IsNullable && !col.IsPrimaryKey {
		parts = append(parts, "NOT NULL")
	}
	if col.IsUnique && !col.IsPrimaryKey {
		parts = append(parts, "UNIQUE")
	}
	if col.DefaultVal != nil {
		parts = append(parts, fmt.Sprintf("DEFAULT %s", formatDefaultVal(col.DefaultVal)))
	}
	return strings.Join(parts, " ")
}

func postgresTypeMapping(dt DataType, length int) string {
	switch dt {
	case TypeSmallInt:
		return "SMALLINT"
	case TypeInt:
		return "INTEGER"
	case TypeBigInt:
		return "BIGINT"
	case TypeFloat:
		return "REAL"
	case TypeDouble:
		return "DOUBLE PRECISION"
	case TypeBool:
		return "BOOLEAN"
	case TypeString:
		if length > 0 {
			return fmt.Sprintf("VARCHAR(%d)", length)
		}
		return "VARCHAR(255)"
	case TypeText:
		return "TEXT"
	case TypeTimestamp:
		return "TIMESTAMPTZ"
	case TypeJSON:
		return "JSONB"
	case TypeUUID:
		return "UUID"
	case TypeBytes:
		return "BYTEA"
	default:
		return "TEXT"
	}
}

func (p *postgresDialect) SupportsReturning() bool {
	return true
}

func (p *postgresDialect) HasTableSQL(tableName string) (string, any) {
	query := "SELECT 1 FROM information_schema.tables WHERE table_schema = CURRENT_SCHEMA() AND table_name = $1 LIMIT 1"
	return query, tableName
}

func (p *postgresDialect) FormatILike(col string, placeholder string) string {
	return fmt.Sprintf("%s ILIKE %s", col, placeholder)
}

func (p *postgresDialect) JSONExtractSQL(col string, path string) string {
	return fmt.Sprintf("%s->>'%s'", p.QuoteIdentifier(col), path)
}

// sqliteDialect implements Dialect for SQLite engines.
type sqliteDialect struct{}

// NewSQLiteDialect constructs an SQLite dialect compiler.
func NewSQLiteDialect() Dialect {
	return &sqliteDialect{}
}

func (s *sqliteDialect) Kind() DialectKind {
	return DialectSQLite
}

func (s *sqliteDialect) Placeholder(_ int) string {
	// SQLite uses standard question mark positional binding tokens.
	return "?"
}

func (s *sqliteDialect) QuoteIdentifier(name string) string {
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		for i, part := range parts {
			parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
		}
		return strings.Join(parts, ".")
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (s *sqliteDialect) ColumnTypeSQL(col ColumnDef) string {
	// In SQLite, an auto-increment column MUST be declared as INTEGER PRIMARY KEY AUTOINCREMENT.
	// Any other variant fails to trigger the internal rowid sequence mechanics.
	if col.IsAutoIncrement && col.IsPrimaryKey {
		return "INTEGER PRIMARY KEY AUTOINCREMENT"
	}

	sqlType := sqliteTypeMapping(col.DataType)
	var parts []string
	parts = append(parts, sqlType)

	if col.IsPrimaryKey && !col.IsAutoIncrement {
		parts = append(parts, "PRIMARY KEY")
	}
	if !col.IsNullable && !col.IsPrimaryKey {
		parts = append(parts, "NOT NULL")
	}
	if col.IsUnique && !col.IsPrimaryKey {
		parts = append(parts, "UNIQUE")
	}
	if col.DefaultVal != nil {
		parts = append(parts, fmt.Sprintf("DEFAULT %s", formatDefaultVal(col.DefaultVal)))
	}
	return strings.Join(parts, " ")
}

func sqliteTypeMapping(dt DataType) string {
	switch dt {
	case TypeSmallInt, TypeInt, TypeBigInt:
		return "INTEGER"
	case TypeFloat, TypeDouble:
		return "REAL"
	case TypeBool:
		return "INTEGER"
	case TypeBytes:
		return "BLOB"
	case TypeString, TypeText, TypeTimestamp, TypeJSON, TypeUUID:
		return "TEXT"
	default:
		return "TEXT"
	}
}

func (s *sqliteDialect) SupportsReturning() bool {
	// SQLite introduced native RETURNING in version 3.35.0 (supported by modernc.org/sqlite).
	return true
}

func (s *sqliteDialect) HasTableSQL(tableName string) (string, any) {
	query := "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1"
	return query, tableName
}

func (s *sqliteDialect) FormatILike(col string, placeholder string) string {
	// SQLite LIKE is case-insensitive for ASCII characters by default.
	// Appending COLLATE NOCASE ensures case insensitivity across comparisons.
	return fmt.Sprintf("%s LIKE %s COLLATE NOCASE", col, placeholder)
}

func (s *sqliteDialect) JSONExtractSQL(col string, path string) string {
	p := path
	if !strings.HasPrefix(p, "$.") && !strings.HasPrefix(p, "$") {
		p = "$." + p
	}
	return fmt.Sprintf("json_extract(%s, '%s')", s.QuoteIdentifier(col), p)
}

func formatDefaultVal(v any) string {
	switch val := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case bool:
		if val {
			return "TRUE"
		}
		return "FALSE"
	default:
		return fmt.Sprintf("%v", val)
	}
}
