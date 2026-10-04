package duosql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// DataType represents standard abstract column data types across database engines.
type DataType string

const (
	TypeSmallInt  DataType = "smallint"
	TypeInt       DataType = "integer"
	TypeBigInt    DataType = "bigint"
	TypeFloat     DataType = "float"
	TypeDouble    DataType = "double"
	TypeBool      DataType = "boolean"
	TypeString    DataType = "string"
	TypeText      DataType = "text"
	TypeTimestamp DataType = "timestamp"
	TypeJSON      DataType = "json"
	TypeUUID      DataType = "uuid"
	TypeBytes     DataType = "bytes"
)

// ColumnDef specifies physical properties of a table column.
type ColumnDef struct {
	Name            string
	DataType        DataType
	Length          int
	IsPrimaryKey    bool
	IsAutoIncrement bool
	IsNullable      bool
	IsUnique        bool
	DefaultVal      any
}

// ForeignKeyDef describes a relational constraint pointing to another table.
type ForeignKeyDef struct {
	Column           string
	ReferencedTable  string
	ReferencedColumn string
	OnDeleteAction   string
	OnUpdateAction   string
}

type tableIndexDef struct {
	name    string
	columns []string
	unique  bool
	where   string
}

// TableBuilder records column declarations and table-level constraints during DDL construction.
type TableBuilder struct {
	tableName    string
	columns      []*ColumnBuilder
	foreignKeys  []*ForeignKeyDef
	compositePKs []string
	uniqueGroups [][]string
	indexes      []*tableIndexDef
}

// ColumnBuilder provides a fluent interface for configuring column metadata.
type ColumnBuilder struct {
	def ColumnDef
}

// PrimaryKey marks the column as the primary key of the table.
func (c *ColumnBuilder) PrimaryKey() *ColumnBuilder {
	c.def.IsPrimaryKey = true
	return c
}

// AutoIncrement configures automatic sequence generation for numeric primary keys.
func (c *ColumnBuilder) AutoIncrement() *ColumnBuilder {
	c.def.IsAutoIncrement = true
	c.def.IsPrimaryKey = true
	return c
}

// NotNull enforces a non-nullable constraint on the column.
func (c *ColumnBuilder) NotNull() *ColumnBuilder {
	c.def.IsNullable = false
	return c
}

// Nullable explicitly marks the column as nullable.
func (c *ColumnBuilder) Nullable() *ColumnBuilder {
	c.def.IsNullable = true
	return c
}

// Unique attaches a unique constraint to this individual column.
func (c *ColumnBuilder) Unique() *ColumnBuilder {
	c.def.IsUnique = true
	return c
}

// Default assigns a constant default value evaluated at insert time.
func (c *ColumnBuilder) Default(val any) *ColumnBuilder {
	c.def.DefaultVal = val
	return c
}

// ID creates a conventional 64-bit auto-incrementing primary key named "id".
func (t *TableBuilder) ID() *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:            "id",
			DataType:        TypeBigInt,
			IsPrimaryKey:    true,
			IsAutoIncrement: true,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// String adds a variable-length string column with an optional length specification.
func (t *TableBuilder) String(name string, length ...int) *ColumnBuilder {
	l := 255
	if len(length) > 0 && length[0] > 0 {
		l = length[0]
	}
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeString,
			Length:   l,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// Text declares an unbounded text column.
func (t *TableBuilder) Text(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeText,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// Int declares a 32-bit signed integer column.
func (t *TableBuilder) Int(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeInt,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// BigInt declares a 64-bit signed integer column.
func (t *TableBuilder) BigInt(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeBigInt,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// SmallInt declares a 16-bit signed integer column.
func (t *TableBuilder) SmallInt(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeSmallInt,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// Float declares a single-precision floating point column.
func (t *TableBuilder) Float(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeFloat,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// Double declares a double-precision floating point column.
func (t *TableBuilder) Double(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeDouble,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// Bool declares a boolean column.
func (t *TableBuilder) Bool(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeBool,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// Timestamp declares a timestamp column with timezone awareness when available.
func (t *TableBuilder) Timestamp(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeTimestamp,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// JSON declares a structured JSON column (PostgreSQL JSONB, SQLite TEXT).
func (t *TableBuilder) JSON(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeJSON,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// UUID declares a unique identifier column.
func (t *TableBuilder) UUID(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeUUID,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// Bytes declares a binary data column (PostgreSQL BYTEA, SQLite BLOB).
func (t *TableBuilder) Bytes(name string) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: TypeBytes,
		},
	}
	t.columns = append(t.columns, cb)
	return cb
}

// ForeignKeyBuilder configures referential action cascades.
type ForeignKeyBuilder struct {
	def *ForeignKeyDef
}

// OnDelete specifies the referential action triggered when parent rows are deleted.
func (f *ForeignKeyBuilder) OnDelete(action string) *ForeignKeyBuilder {
	f.def.OnDeleteAction = strings.ToUpper(action)
	return f
}

// OnUpdate specifies the referential action triggered when parent key values change.
func (f *ForeignKeyBuilder) OnUpdate(action string) *ForeignKeyBuilder {
	f.def.OnUpdateAction = strings.ToUpper(action)
	return f
}

// ForeignKey adds a referential constraint linking col to refTable(refCol).
func (t *TableBuilder) ForeignKey(col, refTable, refCol string) *ForeignKeyBuilder {
	fk := &ForeignKeyDef{
		Column:           col,
		ReferencedTable:  refTable,
		ReferencedColumn: refCol,
		OnDeleteAction:   "NO ACTION",
		OnUpdateAction:   "NO ACTION",
	}
	t.foreignKeys = append(t.foreignKeys, fk)
	return &ForeignKeyBuilder{def: fk}
}

// PrimaryKey registers a multi-column composite primary key constraint.
func (t *TableBuilder) PrimaryKey(cols ...string) {
	t.compositePKs = append(t.compositePKs, cols...)
}

// Unique registers a composite unique constraint over the specified columns.
func (t *TableBuilder) Unique(cols ...string) {
	if len(cols) > 0 {
		t.uniqueGroups = append(t.uniqueGroups, cols)
	}
}

// Timestamps adds standard created_at and updated_at TIMESTAMP columns with CURRENT_TIMESTAMP default.
func (t *TableBuilder) Timestamps() {
	t.Timestamp("created_at").NotNull().Default("CURRENT_TIMESTAMP")
	t.Timestamp("updated_at").NotNull().Default("CURRENT_TIMESTAMP")
}

// Index registers a named index on the table.
func (t *TableBuilder) Index(name string, cols ...string) *TableBuilder {
	t.indexes = append(t.indexes, &tableIndexDef{
		name:    name,
		columns: cols,
	})
	return t
}

// UniqueIndex registers a named unique index on the table.
func (t *TableBuilder) UniqueIndex(name string, cols ...string) *TableBuilder {
	t.indexes = append(t.indexes, &tableIndexDef{
		name:    name,
		columns: cols,
		unique:  true,
	})
	return t
}

// Proto registers a binary protobuf wire column (PostgreSQL BYTEA, SQLite BLOB).
func (t *TableBuilder) Proto(name string) *ColumnBuilder {
	return t.Bytes(name)
}

// SchemaBuilder provides DDL execution workflows for database migrations.
type SchemaBuilder struct {
	executor ExecExecutor
	dialect  Dialect
}

// NewSchemaBuilder constructs a schema manager bound to an active database session.
func NewSchemaBuilder(executor ExecExecutor, dialect Dialect) *SchemaBuilder {
	return &SchemaBuilder{
		executor: executor,
		dialect:  dialect,
	}
}

// CreateTable starts table definition using a declarative builder callback.
func (s *SchemaBuilder) CreateTable(name string, fn func(t *TableBuilder)) *CreateTableBuilder {
	tb := &TableBuilder{tableName: name}
	fn(tb)
	return &CreateTableBuilder{
		schema: s,
		table:  tb,
	}
}

// CreateTableIfNotExists initiates table definition with IF NOT EXISTS automatically enabled.
func (s *SchemaBuilder) CreateTableIfNotExists(name string, fn func(t *TableBuilder)) *CreateTableBuilder {
	return s.CreateTable(name, fn).IfNotExists()
}

// DropTable initiates a table removal statement.
func (s *SchemaBuilder) DropTable(name string) *DropTableBuilder {
	return &DropTableBuilder{
		schema: s,
		name:   name,
	}
}

// CreateIndex initiates an index creation statement.
func (s *SchemaBuilder) CreateIndex(name string) *CreateIndexBuilder {
	return &CreateIndexBuilder{
		schema: s,
		name:   name,
	}
}

// AlterTable initiates table modification operations.
func (s *SchemaBuilder) AlterTable(name string, fn func(a *AlterTableBuilder)) *AlterTableBuilder {
	ab := &AlterTableBuilder{
		schema:    s,
		tableName: name,
	}
	fn(ab)
	return ab
}

// HasTable reports whether a table exists in the current database schema.
func (s *SchemaBuilder) HasTable(ctx context.Context, tableName string) (bool, error) {
	query, arg := s.dialect.HasTableSQL(tableName)
	var dummy int
	err := s.executor.QueryRowContext(ctx, query, arg).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("duosql: check table existence: %w", err)
	}
	return true, nil
}

// HasColumn reports whether the specified column exists within a database table.
func (s *SchemaBuilder) HasColumn(ctx context.Context, tableName, columnName string) (bool, error) {
	if s.dialect.Kind() == DialectSQLite {
		rows, err := s.executor.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", s.dialect.QuoteIdentifier(tableName)))
		if err != nil {
			return false, err
		}
		defer rows.Close()

		for rows.Next() {
			var cid int
			var name, cType string
			var notNull, pk int
			var dfltValue any
			if err := rows.Scan(&cid, &name, &cType, &notNull, &dfltValue, &pk); err == nil {
				if strings.EqualFold(name, columnName) {
					return true, nil
				}
			}
		}
		return false, rows.Err()
	}

	var exists bool
	err := s.executor.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns 
			WHERE table_name = $1 AND column_name = $2
		)
	`, strings.ToLower(tableName), strings.ToLower(columnName)).Scan(&exists)
	return exists, err
}

// HasIndex reports whether the specified index exists on the database table.
func (s *SchemaBuilder) HasIndex(ctx context.Context, tableName, indexName string) (bool, error) {
	if s.dialect.Kind() == DialectSQLite {
		rows, err := s.executor.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%s)", s.dialect.QuoteIdentifier(tableName)))
		if err != nil {
			return false, err
		}
		defer rows.Close()

		for rows.Next() {
			var seq int
			var name string
			var unique int
			var origin string
			var partial int
			if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err == nil {
				if strings.EqualFold(name, indexName) {
					return true, nil
				}
			}
		}
		return false, rows.Err()
	}

	var exists bool
	err := s.executor.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes 
			WHERE (schemaname = current_schema() OR schemaname = 'public') 
			  AND LOWER(tablename) = $1 
			  AND LOWER(indexname) = $2
		)
	`, strings.ToLower(tableName), strings.ToLower(indexName)).Scan(&exists)
	return exists, err
}

// DropIndex initiates an index removal statement.
func (s *SchemaBuilder) DropIndex(name string) *DropIndexBuilder {
	return &DropIndexBuilder{
		schema: s,
		name:   name,
	}
}

// CreateTableBuilder compiles and executes table creation DDL.
type CreateTableBuilder struct {
	schema      *SchemaBuilder
	table       *TableBuilder
	ifNotExists bool
}

// IfNotExists adds the IF NOT EXISTS clause to the DDL statement.
func (b *CreateTableBuilder) IfNotExists() *CreateTableBuilder {
	b.ifNotExists = true
	return b
}

// Build generates the executable DDL statement.
func (b *CreateTableBuilder) Build() (string, error) {
	if len(b.table.columns) == 0 {
		return "", errors.New("duosql: cannot create table without columns")
	}

	var clauses []string
	d := b.schema.dialect

	for _, col := range b.table.columns {
		colSQL := fmt.Sprintf("%s %s", d.QuoteIdentifier(col.def.Name), d.ColumnTypeSQL(col.def))
		clauses = append(clauses, colSQL)
	}

	if len(b.table.compositePKs) > 0 {
		var quoted []string
		for _, col := range b.table.compositePKs {
			quoted = append(quoted, d.QuoteIdentifier(col))
		}
		clauses = append(clauses, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(quoted, ", ")))
	}

	for _, uq := range b.table.uniqueGroups {
		var quoted []string
		for _, col := range uq {
			quoted = append(quoted, d.QuoteIdentifier(col))
		}
		clauses = append(clauses, fmt.Sprintf("UNIQUE (%s)", strings.Join(quoted, ", ")))
	}

	for _, fk := range b.table.foreignKeys {
		fkSQL := fmt.Sprintf(
			"FOREIGN KEY (%s) REFERENCES %s (%s) ON DELETE %s ON UPDATE %s",
			d.QuoteIdentifier(fk.Column),
			d.QuoteIdentifier(fk.ReferencedTable),
			d.QuoteIdentifier(fk.ReferencedColumn),
			fk.OnDeleteAction,
			fk.OnUpdateAction,
		)
		clauses = append(clauses, fkSQL)
	}

	notExistsClause := ""
	if b.ifNotExists {
		notExistsClause = "IF NOT EXISTS "
	}

	sqlStr := fmt.Sprintf(
		"CREATE TABLE %s%s (\n  %s\n);",
		notExistsClause,
		d.QuoteIdentifier(b.table.tableName),
		strings.Join(clauses, ",\n  "),
	)
	return sqlStr, nil
}

// Exec executes the table creation statement against the database and provisions declared indexes.
func (b *CreateTableBuilder) Exec(ctx context.Context) error {
	query, err := b.Build()
	if err != nil {
		return err
	}
	_, err = b.schema.executor.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("duosql: execute create table: %w", err)
	}

	for _, idx := range b.table.indexes {
		ib := b.schema.CreateIndex(idx.name).On(b.table.tableName, idx.columns...).IfNotExists()
		if idx.unique {
			ib.Unique()
		}
		if idx.where != "" {
			ib.Where(idx.where)
		}
		if err := ib.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// DropTableBuilder compiles and executes table drop DDL.
type DropTableBuilder struct {
	schema   *SchemaBuilder
	name     string
	ifExists bool
	cascade  bool
}

// IfExists adds IF EXISTS to the DROP TABLE statement.
func (b *DropTableBuilder) IfExists() *DropTableBuilder {
	b.ifExists = true
	return b
}

// Cascade appends CASCADE to clean dependent constraints (Postgres).
func (b *DropTableBuilder) Cascade() *DropTableBuilder {
	b.cascade = true
	return b
}

// Build generates the drop statement string.
func (b *DropTableBuilder) Build() string {
	var parts []string
	parts = append(parts, "DROP TABLE")
	if b.ifExists {
		parts = append(parts, "IF EXISTS")
	}
	parts = append(parts, b.schema.dialect.QuoteIdentifier(b.name))
	if b.cascade && b.schema.dialect.Kind() == DialectPostgres {
		parts = append(parts, "CASCADE")
	}
	return strings.Join(parts, " ") + ";"
}

// Exec executes the drop table command.
func (b *DropTableBuilder) Exec(ctx context.Context) error {
	query := b.Build()
	_, err := b.schema.executor.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("duosql: execute drop table: %w", err)
	}
	return nil
}

// CreateIndexBuilder compiles index creation statements.
type CreateIndexBuilder struct {
	schema      *SchemaBuilder
	name        string
	tableName   string
	columns     []string
	isUnique    bool
	ifNotExists bool
	where       string
}

// On binds the target table and columns to index.
func (b *CreateIndexBuilder) On(table string, cols ...string) *CreateIndexBuilder {
	b.tableName = table
	b.columns = append(b.columns, cols...)
	return b
}

// Unique marks this index as UNIQUE.
func (b *CreateIndexBuilder) Unique() *CreateIndexBuilder {
	b.isUnique = true
	return b
}

// IfNotExists prevents errors if the index already exists.
func (b *CreateIndexBuilder) IfNotExists() *CreateIndexBuilder {
	b.ifNotExists = true
	return b
}

// Where specifies a partial index predicate expression.
func (b *CreateIndexBuilder) Where(condition string) *CreateIndexBuilder {
	b.where = condition
	return b
}

// Build compiles the CREATE INDEX SQL string.
func (b *CreateIndexBuilder) Build() (string, error) {
	if b.tableName == "" || len(b.columns) == 0 {
		return "", errors.New("duosql: index requires a table name and at least one column")
	}
	d := b.schema.dialect
	var parts []string
	parts = append(parts, "CREATE")
	if b.isUnique {
		parts = append(parts, "UNIQUE")
	}
	parts = append(parts, "INDEX")
	if b.ifNotExists {
		parts = append(parts, "IF NOT EXISTS")
	}
	parts = append(parts, d.QuoteIdentifier(b.name))
	parts = append(parts, "ON", d.QuoteIdentifier(b.tableName))

	var quotedCols []string
	for _, col := range b.columns {
		quotedCols = append(quotedCols, d.QuoteIdentifier(col))
	}
	parts = append(parts, fmt.Sprintf("(%s)", strings.Join(quotedCols, ", ")))
	if b.where != "" {
		parts = append(parts, fmt.Sprintf("WHERE %s", b.where))
	}
	return strings.Join(parts, " ") + ";", nil
}

// Exec executes the index creation.
func (b *CreateIndexBuilder) Exec(ctx context.Context) error {
	query, err := b.Build()
	if err != nil {
		return err
	}
	_, err = b.schema.executor.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("duosql: execute create index: %w", err)
	}
	return nil
}

// DropIndexBuilder compiles DROP INDEX statements.
type DropIndexBuilder struct {
	schema   *SchemaBuilder
	name     string
	ifExists bool
	cascade  bool
}

// IfExists prevents errors if the index does not exist.
func (b *DropIndexBuilder) IfExists() *DropIndexBuilder {
	b.ifExists = true
	return b
}

// Cascade drops objects that depend on this index (PostgreSQL).
func (b *DropIndexBuilder) Cascade() *DropIndexBuilder {
	b.cascade = true
	return b
}

// Build generates the DROP INDEX SQL statement.
func (b *DropIndexBuilder) Build() string {
	var parts []string
	parts = append(parts, "DROP INDEX")
	if b.ifExists {
		parts = append(parts, "IF EXISTS")
	}
	parts = append(parts, b.schema.dialect.QuoteIdentifier(b.name))
	if b.cascade && b.schema.dialect.Kind() == DialectPostgres {
		parts = append(parts, "CASCADE")
	}
	return strings.Join(parts, " ") + ";"
}

// Exec executes index removal.
func (b *DropIndexBuilder) Exec(ctx context.Context) error {
	query := b.Build()
	_, err := b.schema.executor.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("duosql: execute drop index: %w", err)
	}
	return nil
}

// CreateTableFromModel generates and executes table schema and declared indexes from struct type T.
func CreateTableFromModel[T any](ctx context.Context, s *SchemaBuilder, ifNotExists ...bool) error {
	meta, err := GetModelMetadata[T]()
	if err != nil {
		return err
	}

	tb := s.CreateTable(meta.TableName, func(t *TableBuilder) {
		for _, f := range meta.Fields {
			col := buildColumnFromField(t, f)
			if f.IsPrimaryKey {
				if f.IsAuto {
					col.AutoIncrement()
				} else {
					col.PrimaryKey()
				}
			}
			if f.IsUniqueIndex {
				col.Unique()
			}
		}
		for _, idx := range meta.Indexes {
			if idx.Unique {
				t.UniqueIndex(idx.Name, idx.Columns...)
			} else {
				t.Index(idx.Name, idx.Columns...)
			}
		}
	})

	if len(ifNotExists) > 0 && ifNotExists[0] {
		tb.IfNotExists()
	}
	return tb.Exec(ctx)
}

func buildColumnFromField(t *TableBuilder, f FieldInfo) *ColumnBuilder {
	if f.IsProto {
		return t.Bytes(f.ColumnName)
	}
	if f.IsJSON {
		return t.JSON(f.ColumnName)
	}
	if f.FieldType == timeType || f.FieldType == timePtrType {
		return t.Timestamp(f.ColumnName)
	}

	switch f.FieldType.Kind() {
	case reflect.Int, reflect.Int32:
		return t.Int(f.ColumnName)
	case reflect.Int64:
		return t.BigInt(f.ColumnName)
	case reflect.Int16:
		return t.SmallInt(f.ColumnName)
	case reflect.Float32:
		return t.Float(f.ColumnName)
	case reflect.Float64:
		return t.Double(f.ColumnName)
	case reflect.Bool:
		return t.Bool(f.ColumnName)
	case reflect.Slice:
		if f.FieldType.Elem().Kind() == reflect.Uint8 {
			return t.Bytes(f.ColumnName)
		}
		return t.JSON(f.ColumnName)
	default:
		return t.Text(f.ColumnName)
	}
}

type alterActionKind int

const (
	alterAddColumn alterActionKind = iota
	alterDropColumn
	alterRenameColumn
	alterRenameTable
)

type alterAction struct {
	kind    alterActionKind
	column  *ColumnBuilder
	oldName string
	newName string
}

// AlterTableBuilder manages ALTER TABLE operations.
type AlterTableBuilder struct {
	schema    *SchemaBuilder
	tableName string
	actions   []alterAction
}

// AddColumn appends a new column to the table.
func (a *AlterTableBuilder) AddColumn(name string, dt DataType) *ColumnBuilder {
	cb := &ColumnBuilder{
		def: ColumnDef{
			Name:     name,
			DataType: dt,
		},
	}
	a.actions = append(a.actions, alterAction{
		kind:   alterAddColumn,
		column: cb,
	})
	return cb
}

// DropColumn removes an existing column.
func (a *AlterTableBuilder) DropColumn(name string) *AlterTableBuilder {
	a.actions = append(a.actions, alterAction{
		kind:    alterDropColumn,
		oldName: name,
	})
	return a
}

// RenameColumn renames an existing column.
func (a *AlterTableBuilder) RenameColumn(oldName, newName string) *AlterTableBuilder {
	a.actions = append(a.actions, alterAction{
		kind:    alterRenameColumn,
		oldName: oldName,
		newName: newName,
	})
	return a
}

// RenameTable renames the table.
func (a *AlterTableBuilder) RenameTable(newName string) *AlterTableBuilder {
	a.actions = append(a.actions, alterAction{
		kind:    alterRenameTable,
		newName: newName,
	})
	return a
}

// Build generates the ALTER TABLE SQL statements.
func (a *AlterTableBuilder) Build() ([]string, error) {
	if len(a.actions) == 0 {
		return nil, errors.New("duosql: alter table requires at least one action")
	}

	d := a.schema.dialect
	var queries []string

	for _, action := range a.actions {
		switch action.kind {
		case alterAddColumn:
			colSQL := fmt.Sprintf("%s %s", d.QuoteIdentifier(action.column.def.Name), d.ColumnTypeSQL(action.column.def))
			queries = append(queries, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", d.QuoteIdentifier(a.tableName), colSQL))
		case alterDropColumn:
			queries = append(queries, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", d.QuoteIdentifier(a.tableName), d.QuoteIdentifier(action.oldName)))
		case alterRenameColumn:
			queries = append(queries, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;", d.QuoteIdentifier(a.tableName), d.QuoteIdentifier(action.oldName), d.QuoteIdentifier(action.newName)))
		case alterRenameTable:
			queries = append(queries, fmt.Sprintf("ALTER TABLE %s RENAME TO %s;", d.QuoteIdentifier(a.tableName), d.QuoteIdentifier(action.newName)))
		}
	}
	return queries, nil
}

// Exec executes all compiled ALTER TABLE statements sequentially.
func (a *AlterTableBuilder) Exec(ctx context.Context) error {
	queries, err := a.Build()
	if err != nil {
		return err
	}
	for _, q := range queries {
		if _, err := a.schema.executor.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("duosql: alter table exec %q: %w", q, err)
		}
	}
	return nil
}
