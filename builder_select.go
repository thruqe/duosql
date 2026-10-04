package duosql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"strings"
)

// joinClause holds join specification for a table.
type joinClause struct {
	joinType string
	table    string
	on       Predicate
}

// SelectBuilder constructs and executes SELECT queries with generic entity decoding.
type SelectBuilder[T any] struct {
	executor       QueryExecutor
	columns        []string
	fromTable      string
	alias          string
	distinct       bool
	joins          []joinClause
	where          []Predicate
	groupBy        []string
	having         []Predicate
	orderBy        []OrderByExpr
	limit          *int
	offset         *int
	forUpdate      bool
	includeTrashed bool
	onlyTrashed    bool
}

// Select initializes a generic query builder for model entity type T.
func Select[T any](executor QueryExecutor, cols ...string) *SelectBuilder[T] {
	return &SelectBuilder[T]{
		executor: executor,
		columns:  cols,
	}
}

// WithTrashed instructs the query to include soft-deleted records.
func (b *SelectBuilder[T]) WithTrashed() *SelectBuilder[T] {
	b.includeTrashed = true
	b.onlyTrashed = false
	return b
}

// OnlyTrashed filters the query to only return soft-deleted records.
func (b *SelectBuilder[T]) OnlyTrashed() *SelectBuilder[T] {
	b.onlyTrashed = true
	b.includeTrashed = false
	return b
}

// Paginate calculates LIMIT and OFFSET from 1-based page and pageSize parameters.
func (b *SelectBuilder[T]) Paginate(page int, pageSize int) *SelectBuilder[T] {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	b.limit = &pageSize
	b.offset = &offset
	return b
}

// From sets the primary source table for the query.
func (b *SelectBuilder[T]) From(table string) *SelectBuilder[T] {
	b.fromTable = table
	return b
}

// As sets an alias for the primary table.
func (b *SelectBuilder[T]) As(alias string) *SelectBuilder[T] {
	b.alias = alias
	return b
}

// Distinct ensures duplicate rows are eliminated from the result set.
func (b *SelectBuilder[T]) Distinct() *SelectBuilder[T] {
	b.distinct = true
	return b
}

// Where appends filtering conditions to the WHERE clause.
func (b *SelectBuilder[T]) Where(preds ...Predicate) *SelectBuilder[T] {
	b.where = append(b.where, preds...)
	return b
}

// Join appends an INNER JOIN clause.
func (b *SelectBuilder[T]) Join(table string, on Predicate) *SelectBuilder[T] {
	b.joins = append(b.joins, joinClause{joinType: "INNER JOIN", table: table, on: on})
	return b
}

// LeftJoin appends a LEFT OUTER JOIN clause.
func (b *SelectBuilder[T]) LeftJoin(table string, on Predicate) *SelectBuilder[T] {
	b.joins = append(b.joins, joinClause{joinType: "LEFT JOIN", table: table, on: on})
	return b
}

// RightJoin appends a RIGHT OUTER JOIN clause.
func (b *SelectBuilder[T]) RightJoin(table string, on Predicate) *SelectBuilder[T] {
	b.joins = append(b.joins, joinClause{joinType: "RIGHT JOIN", table: table, on: on})
	return b
}

// FullJoin appends a FULL OUTER JOIN clause.
func (b *SelectBuilder[T]) FullJoin(table string, on Predicate) *SelectBuilder[T] {
	b.joins = append(b.joins, joinClause{joinType: "FULL JOIN", table: table, on: on})
	return b
}

// CrossJoin appends a CROSS JOIN clause.
func (b *SelectBuilder[T]) CrossJoin(table string) *SelectBuilder[T] {
	b.joins = append(b.joins, joinClause{joinType: "CROSS JOIN", table: table, on: nil})
	return b
}

// GroupBy adds columns to the GROUP BY clause.
func (b *SelectBuilder[T]) GroupBy(cols ...string) *SelectBuilder[T] {
	b.groupBy = append(b.groupBy, cols...)
	return b
}

// Having appends aggregation filters to the HAVING clause.
func (b *SelectBuilder[T]) Having(preds ...Predicate) *SelectBuilder[T] {
	b.having = append(b.having, preds...)
	return b
}

// OrderBy appends ordering criteria.
func (b *SelectBuilder[T]) OrderBy(exprs ...OrderByExpr) *SelectBuilder[T] {
	b.orderBy = append(b.orderBy, exprs...)
	return b
}

// Asc sorts by column in ascending order.
func (b *SelectBuilder[T]) Asc(col string) *SelectBuilder[T] {
	b.orderBy = append(b.orderBy, Asc(col))
	return b
}

// Desc sorts by column in descending order.
func (b *SelectBuilder[T]) Desc(col string) *SelectBuilder[T] {
	b.orderBy = append(b.orderBy, Desc(col))
	return b
}

// Limit sets the maximum number of records to return.
func (b *SelectBuilder[T]) Limit(n int) *SelectBuilder[T] {
	b.limit = &n
	return b
}

// Offset sets the number of records to skip before reading.
func (b *SelectBuilder[T]) Offset(n int) *SelectBuilder[T] {
	b.offset = &n
	return b
}

// ForUpdate attaches a row lock directive when supported by the database engine.
func (b *SelectBuilder[T]) ForUpdate() *SelectBuilder[T] {
	b.forUpdate = true
	return b
}

// Build compiles the configured clauses into a parameterized SQL statement.
func (b *SelectBuilder[T]) Build() (string, []any, error) {
	d := b.executor.Dialect()
	ctx := NewBuildContext(d)

	fromSQL, err := b.compileFromClause(d)
	if err != nil {
		return "", nil, err
	}

	var query strings.Builder
	query.WriteString(fromSQL)

	allArgs, err := b.appendQueryModifiers(&query, ctx)
	if err != nil {
		return "", nil, err
	}

	b.appendPaginationAndLock(&query, d)
	return query.String(), allArgs, nil
}

func (b *SelectBuilder[T]) compileFromClause(d Dialect) (string, error) {
	tableName, err := b.resolveTableName()
	if err != nil {
		return "", err
	}

	colsSQL := b.compileColumns(d)
	var query strings.Builder
	query.WriteString("SELECT ")
	if b.distinct {
		query.WriteString("DISTINCT ")
	}
	query.WriteString(colsSQL)
	query.WriteString(" FROM ")
	query.WriteString(d.QuoteIdentifier(tableName))
	if b.alias != "" {
		query.WriteString(" AS ")
		query.WriteString(d.QuoteIdentifier(b.alias))
	}
	return query.String(), nil
}

func (b *SelectBuilder[T]) appendQueryModifiers(query *strings.Builder, ctx *BuildContext) ([]any, error) {
	var allArgs []any

	if len(b.joins) > 0 {
		joinSQL, joinArgs, err := b.compileJoins(ctx)
		if err != nil {
			return nil, err
		}
		query.WriteString(joinSQL)
		allArgs = append(allArgs, joinArgs...)
	}

	if len(b.where) > 0 {
		whereSQL, whereArgs, err := b.compileWhere(ctx)
		if err != nil {
			return nil, err
		}
		query.WriteString(whereSQL)
		allArgs = append(allArgs, whereArgs...)
	}

	if len(b.groupBy) > 0 {
		query.WriteString(b.compileGroupBy(ctx.Dialect))
	}

	if len(b.having) > 0 {
		havingSQL, havingArgs, err := b.compileHaving(ctx)
		if err != nil {
			return nil, err
		}
		query.WriteString(havingSQL)
		allArgs = append(allArgs, havingArgs...)
	}

	if len(b.orderBy) > 0 {
		query.WriteString(b.compileOrderBy(ctx))
	}

	return allArgs, nil
}

func (b *SelectBuilder[T]) appendPaginationAndLock(query *strings.Builder, d Dialect) {
	if b.limit != nil {
		query.WriteString(fmt.Sprintf(" LIMIT %d", *b.limit))
	}
	if b.offset != nil {
		query.WriteString(fmt.Sprintf(" OFFSET %d", *b.offset))
	}
	if b.forUpdate && d.Kind() == DialectPostgres {
		query.WriteString(" FOR UPDATE")
	}
}

func (b *SelectBuilder[T]) resolveTableName() (string, error) {
	if b.fromTable != "" {
		return b.fromTable, nil
	}
	meta, err := GetModelMetadata[T]()
	if err != nil {
		return "", fmt.Errorf("duosql: missing FROM table name and cannot derive from model: %w", err)
	}
	return meta.TableName, nil
}

func (b *SelectBuilder[T]) compileColumns(d Dialect) string {
	if len(b.columns) > 0 {
		var quoted []string
		for _, col := range b.columns {
			if strings.Contains(col, "(") || strings.Contains(col, "*") {
				quoted = append(quoted, col)
			} else {
				quoted = append(quoted, d.QuoteIdentifier(col))
			}
		}
		return strings.Join(quoted, ", ")
	}

	meta, err := GetModelMetadata[T]()
	if err == nil && len(meta.Fields) > 0 {
		var quoted []string
		for _, f := range meta.Fields {
			quoted = append(quoted, d.QuoteIdentifier(f.ColumnName))
		}
		return strings.Join(quoted, ", ")
	}

	return "*"
}

func (b *SelectBuilder[T]) compileJoins(ctx *BuildContext) (string, []any, error) {
	var sb strings.Builder
	var args []any
	d := ctx.Dialect

	for _, j := range b.joins {
		sb.WriteString(" ")
		sb.WriteString(j.joinType)
		sb.WriteString(" ")
		sb.WriteString(d.QuoteIdentifier(j.table))
		if j.on != nil {
			sb.WriteString(" ON ")
			onSQL, onArgs, err := j.on.ToSQL(ctx)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(onSQL)
			args = append(args, onArgs...)
		}
	}
	return sb.String(), args, nil
}

func (b *SelectBuilder[T]) compileWhere(ctx *BuildContext) (string, []any, error) {
	preds := b.where
	meta, _ := GetModelMetadata[T]()
	if meta != nil && meta.SoftDeleteColumn != "" {
		if b.onlyTrashed {
			preds = append(preds, IsNotNull(meta.SoftDeleteColumn))
		} else if !b.includeTrashed {
			preds = append(preds, IsNull(meta.SoftDeleteColumn))
		}
	}

	wherePred := And(preds...)
	whereSQL, whereArgs, err := wherePred.ToSQL(ctx)
	if err != nil {
		return "", nil, err
	}
	if whereSQL == "" {
		return "", nil, nil
	}
	return " WHERE " + whereSQL, whereArgs, nil
}

func (b *SelectBuilder[T]) compileGroupBy(d Dialect) string {
	var quoted []string
	for _, col := range b.groupBy {
		quoted = append(quoted, d.QuoteIdentifier(col))
	}
	return " GROUP BY " + strings.Join(quoted, ", ")
}

func (b *SelectBuilder[T]) compileHaving(ctx *BuildContext) (string, []any, error) {
	havingPred := And(b.having...)
	havingSQL, havingArgs, err := havingPred.ToSQL(ctx)
	if err != nil {
		return "", nil, err
	}
	if havingSQL == "" {
		return "", nil, nil
	}
	return " HAVING " + havingSQL, havingArgs, nil
}

func (b *SelectBuilder[T]) compileOrderBy(ctx *BuildContext) string {
	var items []string
	for _, o := range b.orderBy {
		items = append(items, o.ToOrderBySQL(ctx))
	}
	return " ORDER BY " + strings.Join(items, ", ")
}

// One executes the query and unmarshals the first matching row into a newly allocated T.
// Returns sql.ErrNoRows if zero records match.
func (b *SelectBuilder[T]) One(ctx context.Context) (*T, error) {
	// Constrain to single row
	limited := *b
	one := 1
	limited.limit = &one

	query, args, err := limited.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: query: %w", err)
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

// All executes the query and returns all matching rows as a slice of T entities.
func (b *SelectBuilder[T]) All(ctx context.Context) ([]T, error) {
	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: query: %w", err)
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

// Iter yields matching entities as an idiomatic Go range-over-func iterator.
// Designed for memory-efficient streaming over large datasets without buffering the full result set.
func (b *SelectBuilder[T]) Iter(ctx context.Context) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		query, args, err := b.Build()
		if err != nil {
			yield(nil, err)
			return
		}

		rows, err := b.executor.QueryContext(ctx, query, args...)
		if err != nil {
			yield(nil, fmt.Errorf("duosql: query: %w", err))
			return
		}
		defer rows.Close()

		columns, err := rows.Columns()
		if err != nil {
			yield(nil, fmt.Errorf("duosql: read columns: %w", err))
			return
		}

		meta, err := GetModelMetadata[T]()
		if err != nil {
			yield(nil, err)
			return
		}

		for rows.Next() {
			item, err := ScanModel[T](rows, columns, meta)
			if !yield(item, err) {
				return
			}
			if err != nil {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(nil, fmt.Errorf("duosql: row iteration: %w", err))
		}
	}
}

// Count returns the total number of matching rows using a COUNT(*) query wrapper.
func (b *SelectBuilder[T]) Count(ctx context.Context) (int64, error) {
	countBuilder := *b
	countBuilder.columns = []string{"COUNT(*)"}
	countBuilder.orderBy = nil
	countBuilder.limit = nil
	countBuilder.offset = nil

	query, args, err := countBuilder.Build()
	if err != nil {
		return 0, err
	}

	var count int64
	err = b.executor.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("duosql: count query: %w", err)
	}
	return count, nil
}

// Exists checks if at least one matching row satisfies the conditions.
func (b *SelectBuilder[T]) Exists(ctx context.Context) (bool, error) {
	one := 1
	existsBuilder := *b
	existsBuilder.columns = []string{"1"}
	existsBuilder.limit = &one

	query, args, err := existsBuilder.Build()
	if err != nil {
		return false, err
	}

	var dummy int
	err = b.executor.QueryRowContext(ctx, query, args...).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("duosql: exists query: %w", err)
	}
	return true, nil
}

// Pluck extracts a single column from matching rows into a slice of scalar values.
func Pluck[V any, T any](b *SelectBuilder[T], ctx context.Context, column string) ([]V, error) {
	cloned := *b
	cloned.columns = []string{column}

	query, args, err := cloned.Build()
	if err != nil {
		return nil, err
	}

	rows, err := cloned.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: pluck query: %w", err)
	}
	defer rows.Close()

	var results []V
	for rows.Next() {
		var val V
		if err := rows.Scan(&val); err != nil {
			return nil, fmt.Errorf("duosql: pluck scan: %w", err)
		}
		results = append(results, val)
	}
	return results, rows.Err()
}

// Sum computes the sum of the specified numeric column.
func (b *SelectBuilder[T]) Sum(ctx context.Context, column string) (float64, error) {
	sb := *b
	sb.columns = []string{fmt.Sprintf("COALESCE(SUM(%s), 0)", b.executor.Dialect().QuoteIdentifier(column))}
	sb.orderBy = nil
	sb.limit = nil
	sb.offset = nil

	query, args, err := sb.Build()
	if err != nil {
		return 0, err
	}

	var sum float64
	err = b.executor.QueryRowContext(ctx, query, args...).Scan(&sum)
	return sum, err
}

// Avg computes the arithmetic mean of the specified column.
func (b *SelectBuilder[T]) Avg(ctx context.Context, column string) (float64, error) {
	sb := *b
	sb.columns = []string{fmt.Sprintf("COALESCE(AVG(%s), 0)", b.executor.Dialect().QuoteIdentifier(column))}
	sb.orderBy = nil
	sb.limit = nil
	sb.offset = nil

	query, args, err := sb.Build()
	if err != nil {
		return 0, err
	}

	var avg float64
	err = b.executor.QueryRowContext(ctx, query, args...).Scan(&avg)
	return avg, err
}

// Min returns the minimum value of the specified column.
func (b *SelectBuilder[T]) Min(ctx context.Context, column string) (any, error) {
	sb := *b
	sb.columns = []string{fmt.Sprintf("MIN(%s)", b.executor.Dialect().QuoteIdentifier(column))}
	sb.orderBy = nil
	sb.limit = nil
	sb.offset = nil

	query, args, err := sb.Build()
	if err != nil {
		return nil, err
	}

	var minVal any
	err = b.executor.QueryRowContext(ctx, query, args...).Scan(&minVal)
	return minVal, err
}

// Max returns the maximum value of the specified column.
func (b *SelectBuilder[T]) Max(ctx context.Context, column string) (any, error) {
	sb := *b
	sb.columns = []string{fmt.Sprintf("MAX(%s)", b.executor.Dialect().QuoteIdentifier(column))}
	sb.orderBy = nil
	sb.limit = nil
	sb.offset = nil

	query, args, err := sb.Build()
	if err != nil {
		return nil, err
	}

	var maxVal any
	err = b.executor.QueryRowContext(ctx, query, args...).Scan(&maxVal)
	return maxVal, err
}
