package duosql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// setAssignment pairs a column with an updated value expression.
type setAssignment struct {
	column  string
	value   any
	rawExpr string
	rawArgs []any
}

// UpdateBuilder constructs and executes SQL UPDATE statements.
type UpdateBuilder[T any] struct {
	executor      ExecExecutor
	tableName     string
	assignments   []setAssignment
	where         []Predicate
	returningCols []string
}

// Update creates a new UPDATE query builder.
func Update[T any](executor ExecExecutor, table ...string) *UpdateBuilder[T] {
	tName := ""
	if len(table) > 0 {
		tName = table[0]
	}
	return &UpdateBuilder[T]{
		executor:  executor,
		tableName: tName,
	}
}

// Table explicitly specifies the target table name.
func (b *UpdateBuilder[T]) Table(table string) *UpdateBuilder[T] {
	b.tableName = table
	return b
}

// Set adds a single column update assignment.
func (b *UpdateBuilder[T]) Set(column string, value any) *UpdateBuilder[T] {
	b.assignments = append(b.assignments, setAssignment{column: column, value: value})
	return b
}

// Inc increments a numeric column atomically by the specified amount (default 1).
func (b *UpdateBuilder[T]) Inc(column string, amount ...int64) *UpdateBuilder[T] {
	delta := int64(1)
	if len(amount) > 0 {
		delta = amount[0]
	}
	b.assignments = append(b.assignments, setAssignment{
		column:  column,
		rawExpr: fmt.Sprintf("%s + ?", b.executor.Dialect().QuoteIdentifier(column)),
		rawArgs: []any{delta},
	})
	return b
}

// Dec decrements a numeric column atomically by the specified amount (default 1).
func (b *UpdateBuilder[T]) Dec(column string, amount ...int64) *UpdateBuilder[T] {
	delta := int64(1)
	if len(amount) > 0 {
		delta = amount[0]
	}
	b.assignments = append(b.assignments, setAssignment{
		column:  column,
		rawExpr: fmt.Sprintf("%s - ?", b.executor.Dialect().QuoteIdentifier(column)),
		rawArgs: []any{delta},
	})
	return b
}

// SetRaw assigns a column to an unescaped SQL expression.
func (b *UpdateBuilder[T]) SetRaw(column string, expr string, args ...any) *UpdateBuilder[T] {
	b.assignments = append(b.assignments, setAssignment{
		column:  column,
		rawExpr: expr,
		rawArgs: args,
	})
	return b
}

// SetMap merges multiple column assignments from a map.
func (b *UpdateBuilder[T]) SetMap(values map[string]any) *UpdateBuilder[T] {
	for k, v := range values {
		b.assignments = append(b.assignments, setAssignment{column: k, value: v})
	}
	return b
}

// SetModel applies non-primary key fields from an entity model instance.
func (b *UpdateBuilder[T]) SetModel(model *T, cols ...string) *UpdateBuilder[T] {
	meta, err := GetModelMetadata[T]()
	if err != nil {
		return b
	}

	valMap := meta.ExtractInsertMap(reflect.ValueOf(model), false)
	if len(cols) > 0 {
		for _, col := range cols {
			if v, ok := valMap[col]; ok {
				b.assignments = append(b.assignments, setAssignment{column: col, value: v})
			}
		}
		return b
	}

	for _, f := range meta.Fields {
		if !f.IsPrimaryKey && !f.IsAuto {
			b.assignments = append(b.assignments, setAssignment{column: f.ColumnName, value: valMap[f.ColumnName]})
		}
	}
	return b
}

// Where appends filtering conditions to restrict updated rows.
func (b *UpdateBuilder[T]) Where(preds ...Predicate) *UpdateBuilder[T] {
	b.where = append(b.where, preds...)
	return b
}

// Returning requests specified columns to be returned after the update.
func (b *UpdateBuilder[T]) Returning(cols ...string) *UpdateBuilder[T] {
	b.returningCols = append(b.returningCols, cols...)
	return b
}

// Build compiles the UPDATE SQL statement and extracts bound arguments.
func (b *UpdateBuilder[T]) Build() (string, []any, error) {
	if len(b.assignments) == 0 {
		return "", nil, errors.New("duosql: update statement requires at least one Set assignment")
	}

	d := b.executor.Dialect()
	ctx := NewBuildContext(d)

	tableName, err := b.resolveTableName()
	if err != nil {
		return "", nil, err
	}

	var allArgs []any
	var setClauses []string
	for _, a := range b.assignments {
		if a.rawExpr != "" {
			clauseSQL, clauseArgs := compileAssignmentRaw(a, ctx)
			setClauses = append(setClauses, clauseSQL)
			allArgs = append(allArgs, clauseArgs...)
		} else {
			ph := ctx.NextPlaceholder()
			setClauses = append(setClauses, fmt.Sprintf("%s = %s", d.QuoteIdentifier(a.column), ph))
			allArgs = append(allArgs, a.value)
		}
	}

	var sb strings.Builder
	sb.WriteString("UPDATE ")
	sb.WriteString(d.QuoteIdentifier(tableName))
	sb.WriteString(" SET ")
	sb.WriteString(strings.Join(setClauses, ", "))

	if len(b.where) > 0 {
		whereSQL, whereArgs, err := b.compileWhere(ctx)
		if err != nil {
			return "", nil, err
		}
		sb.WriteString(whereSQL)
		allArgs = append(allArgs, whereArgs...)
	}

	if len(b.returningCols) > 0 {
		var quoted []string
		for _, c := range b.returningCols {
			if c == "*" {
				quoted = append(quoted, "*")
			} else {
				quoted = append(quoted, d.QuoteIdentifier(c))
			}
		}
		sb.WriteString(" RETURNING ")
		sb.WriteString(strings.Join(quoted, ", "))
	}

	return sb.String(), allArgs, nil
}

func (b *UpdateBuilder[T]) resolveTableName() (string, error) {
	if b.tableName != "" {
		return b.tableName, nil
	}
	meta, err := GetModelMetadata[T]()
	if err != nil {
		return "", fmt.Errorf("duosql: missing UPDATE table name and cannot derive from model: %w", err)
	}
	return meta.TableName, nil
}

func compileAssignmentRaw(a setAssignment, ctx *BuildContext) (string, []any) {
	exprSQL := a.rawExpr
	var args []any
	for _, arg := range a.rawArgs {
		ph := ctx.NextPlaceholder()
		exprSQL = strings.Replace(exprSQL, "?", ph, 1)
		args = append(args, arg)
	}
	return fmt.Sprintf("%s = %s", ctx.Dialect.QuoteIdentifier(a.column), exprSQL), args
}

func (b *UpdateBuilder[T]) compileWhere(ctx *BuildContext) (string, []any, error) {
	wherePred := And(b.where...)
	whereSQL, whereArgs, err := wherePred.ToSQL(ctx)
	if err != nil {
		return "", nil, err
	}
	if whereSQL == "" {
		return "", nil, nil
	}
	return " WHERE " + whereSQL, whereArgs, nil
}

// Exec executes the update query and returns the count of affected rows.
func (b *UpdateBuilder[T]) Exec(ctx context.Context) (int64, error) {
	query, args, err := b.Build()
	if err != nil {
		return 0, err
	}
	res, err := b.executor.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("duosql: exec update: %w", err)
	}
	return res.RowsAffected()
}

// One executes the update with a RETURNING clause and unmarshals the single affected row.
func (b *UpdateBuilder[T]) One(ctx context.Context) (*T, error) {
	if len(b.returningCols) == 0 {
		b.Returning("*")
	}

	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: update returning: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("duosql: row iteration: %w", err)
		}
		return nil, sql.ErrNoRows
	}

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("duosql: read columns: %w", err)
	}

	meta, err := GetModelMetadata[T]()
	if err != nil {
		return nil, err
	}

	return ScanModel[T](rows, columns, meta)
}

// All executes the update with a RETURNING clause and unmarshals all affected rows.
func (b *UpdateBuilder[T]) All(ctx context.Context) ([]T, error) {
	if len(b.returningCols) == 0 {
		b.Returning("*")
	}

	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: update returning: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("duosql: read columns: %w", err)
	}

	meta, err := GetModelMetadata[T]()
	if err != nil {
		return nil, err
	}

	var results []T
	for rows.Next() {
		item, err := ScanModel[T](rows, columns, meta)
		if err != nil {
			return nil, err
		}
		results = append(results, *item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duosql: row iteration: %w", err)
	}

	return results, nil
}
