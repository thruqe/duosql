package duosql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// DeleteBuilder constructs and executes SQL DELETE statements.
type DeleteBuilder[T any] struct {
	executor      ExecExecutor
	tableName     string
	where         []Predicate
	returningCols []string
	forceDelete   bool
}

// Delete creates a new DELETE query builder.
func Delete[T any](executor ExecExecutor, table ...string) *DeleteBuilder[T] {
	tName := ""
	if len(table) > 0 {
		tName = table[0]
	}
	return &DeleteBuilder[T]{
		executor:  executor,
		tableName: tName,
	}
}

// ForceDelete permanently deletes rows even if the model defines a soft delete column.
func (b *DeleteBuilder[T]) ForceDelete() *DeleteBuilder[T] {
	b.forceDelete = true
	return b
}

// From explicitly specifies the target table name.
func (b *DeleteBuilder[T]) From(table string) *DeleteBuilder[T] {
	b.tableName = table
	return b
}

// Where appends filtering predicates to limit deleted rows.
func (b *DeleteBuilder[T]) Where(preds ...Predicate) *DeleteBuilder[T] {
	b.where = append(b.where, preds...)
	return b
}

// Returning requests specified columns to be returned after the deletion.
func (b *DeleteBuilder[T]) Returning(cols ...string) *DeleteBuilder[T] {
	b.returningCols = append(b.returningCols, cols...)
	return b
}

// Build compiles the DELETE SQL query and returns bound arguments.
func (b *DeleteBuilder[T]) Build() (string, []any, error) {
	d := b.executor.Dialect()
	ctx := NewBuildContext(d)

	tableName, err := b.resolveTableName()
	if err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	sb.WriteString("DELETE FROM ")
	sb.WriteString(d.QuoteIdentifier(tableName))

	var allArgs []any
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

func (b *DeleteBuilder[T]) resolveTableName() (string, error) {
	if b.tableName != "" {
		return b.tableName, nil
	}
	meta, err := GetModelMetadata[T]()
	if err != nil {
		return "", fmt.Errorf("duosql: missing DELETE table name and cannot derive from model: %w", err)
	}
	return meta.TableName, nil
}

func (b *DeleteBuilder[T]) compileWhere(ctx *BuildContext) (string, []any, error) {
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

// Exec executes the deletion statement and returns the number of deleted records.
// If the target entity defines a soft delete timestamp and ForceDelete() was not requested,
// an UPDATE is performed stamping the soft delete column with the current timestamp.
func (b *DeleteBuilder[T]) Exec(ctx context.Context) (int64, error) {
	if !b.forceDelete {
		meta, _ := GetModelMetadata[T]()
		if meta != nil && meta.SoftDeleteColumn != "" {
			return Update[T](b.executor, b.tableName).
				SetRaw(meta.SoftDeleteColumn, "CURRENT_TIMESTAMP").
				Where(b.where...).
				Exec(ctx)
		}
	}

	query, args, err := b.Build()
	if err != nil {
		return 0, err
	}
	res, err := b.executor.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("duosql: exec delete: %w", err)
	}
	return res.RowsAffected()
}

// One executes the deletion with RETURNING and unmarshals the single deleted record.
func (b *DeleteBuilder[T]) One(ctx context.Context) (*T, error) {
	if len(b.returningCols) == 0 {
		b.Returning("*")
	}

	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: delete returning: %w", err)
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

// All executes the deletion with RETURNING and unmarshals all deleted records.
func (b *DeleteBuilder[T]) All(ctx context.Context) ([]T, error) {
	if len(b.returningCols) == 0 {
		b.Returning("*")
	}

	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: delete returning: %w", err)
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
