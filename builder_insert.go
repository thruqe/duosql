package duosql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// InsertBuilder configures and executes SQL INSERT and upsert operations.
type InsertBuilder[T any] struct {
	executor              ExecExecutor
	tableName             string
	columns               []string
	models                []*T
	maps                  []map[string]any
	conflictTargets       []string
	conflictDoNot         bool
	conflictAutoUpdateAll bool
	conflictUpdates       []string
	conflictRawUpdates    []string
	returningCols         []string
}

// Insert creates a new insert query builder for entity type T.
func Insert[T any](executor ExecExecutor, table ...string) *InsertBuilder[T] {
	tName := ""
	if len(table) > 0 {
		tName = table[0]
	}
	return &InsertBuilder[T]{
		executor:  executor,
		tableName: tName,
	}
}

// Into explicitly designates the target table name.
func (b *InsertBuilder[T]) Into(table string) *InsertBuilder[T] {
	b.tableName = table
	return b
}

// Columns overrides the column sequence for values insertion.
func (b *InsertBuilder[T]) Columns(cols ...string) *InsertBuilder[T] {
	b.columns = cols
	return b
}

// Values appends one or more model entity instances to be inserted.
func (b *InsertBuilder[T]) Values(models ...*T) *InsertBuilder[T] {
	b.models = append(b.models, models...)
	return b
}

// ValueMap inserts rows using explicit column-to-value keymaps.
func (b *InsertBuilder[T]) ValueMap(records ...map[string]any) *InsertBuilder[T] {
	b.maps = append(b.maps, records...)
	return b
}

// OnConflictDoNothing appends ON CONFLICT (...) DO NOTHING to skip existing keys.
func (b *InsertBuilder[T]) OnConflictDoNothing(targets ...string) *InsertBuilder[T] {
	b.conflictTargets = targets
	b.conflictDoNot = true
	b.conflictUpdates = nil
	return b
}

// OnConflictDoUpdate configures an upsert to update specified columns when conflict occurs.
func (b *InsertBuilder[T]) OnConflictDoUpdate(targets []string, updateCols []string) *InsertBuilder[T] {
	b.conflictTargets = targets
	b.conflictDoNot = false
	b.conflictAutoUpdateAll = false
	b.conflictUpdates = updateCols
	b.conflictRawUpdates = nil
	return b
}

// OnConflictDoUpdateAll configures an upsert to automatically update all non-conflict columns from the model.
func (b *InsertBuilder[T]) OnConflictDoUpdateAll(targets ...string) *InsertBuilder[T] {
	b.conflictTargets = targets
	b.conflictDoNot = false
	b.conflictAutoUpdateAll = true
	b.conflictUpdates = nil
	b.conflictRawUpdates = nil
	return b
}

// OnConflictDoUpdateRaw configures an upsert to update using explicit SQL assignment expressions.
func (b *InsertBuilder[T]) OnConflictDoUpdateRaw(targets []string, rawAssignments []string) *InsertBuilder[T] {
	b.conflictTargets = targets
	b.conflictDoNot = false
	b.conflictAutoUpdateAll = false
	b.conflictUpdates = nil
	b.conflictRawUpdates = rawAssignments
	return b
}

// Returning requests specified columns to be returned after insertion.
func (b *InsertBuilder[T]) Returning(cols ...string) *InsertBuilder[T] {
	b.returningCols = append(b.returningCols, cols...)
	return b
}

// Build compiles the INSERT SQL statement and extracts bound arguments.
func (b *InsertBuilder[T]) Build() (string, []any, error) {
	d := b.executor.Dialect()
	ctx := NewBuildContext(d)

	tableName, err := b.resolveTableName()
	if err != nil {
		return "", nil, err
	}

	cols, rowsVals, err := b.extractDataMatrix()
	if err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(d.QuoteIdentifier(tableName))
	sb.WriteString(" (")

	var quotedCols []string
	for _, c := range cols {
		quotedCols = append(quotedCols, d.QuoteIdentifier(c))
	}
	sb.WriteString(strings.Join(quotedCols, ", "))
	sb.WriteString(") VALUES ")

	var rowPlaceholders []string
	var allArgs []any
	for _, row := range rowsVals {
		var phs []string
		for _, val := range row {
			phs = append(phs, ctx.NextPlaceholder())
			allArgs = append(allArgs, val)
		}
		rowPlaceholders = append(rowPlaceholders, fmt.Sprintf("(%s)", strings.Join(phs, ", ")))
	}
	sb.WriteString(strings.Join(rowPlaceholders, ", "))

	conflictSQL := b.compileConflict(d)
	if conflictSQL != "" {
		sb.WriteString(" ")
		sb.WriteString(conflictSQL)
	}

	returningSQL := b.compileReturning(d)
	if returningSQL != "" {
		sb.WriteString(" ")
		sb.WriteString(returningSQL)
	}

	return sb.String(), allArgs, nil
}

func (b *InsertBuilder[T]) resolveTableName() (string, error) {
	if b.tableName != "" {
		return b.tableName, nil
	}
	meta, err := GetModelMetadata[T]()
	if err != nil {
		return "", fmt.Errorf("duosql: missing INSERT table name and cannot derive from model: %w", err)
	}
	return meta.TableName, nil
}

func (b *InsertBuilder[T]) extractDataMatrix() ([]string, [][]any, error) {
	if len(b.models) > 0 {
		return b.extractFromModels()
	}
	if len(b.maps) > 0 {
		return b.extractFromMaps()
	}
	return nil, nil, errors.New("duosql: insert statement requires either Values or ValueMap")
}

func (b *InsertBuilder[T]) extractFromModels() ([]string, [][]any, error) {
	meta, err := GetModelMetadata[T]()
	if err != nil {
		return nil, nil, err
	}

	cols := b.columns
	if len(cols) == 0 {
		for _, f := range meta.Fields {
			if !f.IsAuto {
				cols = append(cols, f.ColumnName)
			}
		}
	}
	if len(cols) == 0 {
		return nil, nil, errors.New("duosql: no columns found to insert")
	}

	var rows [][]any
	for _, model := range b.models {
		valMap := meta.ExtractInsertMap(reflect.ValueOf(model), true)
		var row []any
		for _, col := range cols {
			row = append(row, valMap[col])
		}
		rows = append(rows, row)
	}

	return cols, rows, nil
}

func (b *InsertBuilder[T]) extractFromMaps() ([]string, [][]any, error) {
	cols := b.columns
	if len(cols) == 0 {
		// Use keys from first map entry
		for k := range b.maps[0] {
			cols = append(cols, k)
		}
	}

	var rows [][]any
	for _, m := range b.maps {
		var row []any
		for _, col := range cols {
			row = append(row, m[col])
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

func (b *InsertBuilder[T]) resolveConflictUpdateCols() []string {
	if b.conflictAutoUpdateAll {
		meta, err := GetModelMetadata[T]()
		if err != nil {
			return b.conflictUpdates
		}
		targetSet := make(map[string]bool, len(b.conflictTargets))
		for _, t := range b.conflictTargets {
			targetSet[t] = true
		}
		updates := make([]string, 0, len(meta.Fields))
		for _, f := range meta.Fields {
			if !targetSet[f.ColumnName] && !f.IsPrimaryKey && !f.IsCreatedAt {
				updates = append(updates, f.ColumnName)
			}
		}
		return updates
	}

	updates := b.conflictUpdates
	if len(updates) > 0 {
		if meta, err := GetModelMetadata[T](); err == nil && meta.UpdatedAtCol != "" {
			if !slices.Contains(updates, meta.UpdatedAtCol) {
				updates = append(updates, meta.UpdatedAtCol)
			}
		}
	}
	return updates
}

func (b *InsertBuilder[T]) compileConflict(d Dialect) string {
	if !b.conflictDoNot && len(b.conflictUpdates) == 0 && len(b.conflictRawUpdates) == 0 && !b.conflictAutoUpdateAll {
		return ""
	}

	var targetClause string
	if len(b.conflictTargets) > 0 {
		var quoted []string
		for _, t := range b.conflictTargets {
			quoted = append(quoted, d.QuoteIdentifier(t))
		}
		targetClause = fmt.Sprintf("(%s)", strings.Join(quoted, ", "))
	}

	if b.conflictDoNot {
		if targetClause != "" {
			return fmt.Sprintf("ON CONFLICT %s DO NOTHING", targetClause)
		}
		return "ON CONFLICT DO NOTHING"
	}

	if len(b.conflictRawUpdates) > 0 {
		return fmt.Sprintf("ON CONFLICT %s DO UPDATE SET %s", targetClause, strings.Join(b.conflictRawUpdates, ", "))
	}

	updates := b.resolveConflictUpdateCols()
	var sets []string
	for _, col := range updates {
		qCol := d.QuoteIdentifier(col)
		sets = append(sets, fmt.Sprintf("%s = EXCLUDED.%s", qCol, qCol))
	}
	return fmt.Sprintf("ON CONFLICT %s DO UPDATE SET %s", targetClause, strings.Join(sets, ", "))
}

func (b *InsertBuilder[T]) compileReturning(d Dialect) string {
	if len(b.returningCols) == 0 {
		return ""
	}
	var quoted []string
	for _, col := range b.returningCols {
		if col == "*" {
			quoted = append(quoted, "*")
		} else {
			quoted = append(quoted, d.QuoteIdentifier(col))
		}
	}
	return fmt.Sprintf("RETURNING %s", strings.Join(quoted, ", "))
}

// Exec executes the insertion command, returning the standard sql.Result.
func (b *InsertBuilder[T]) Exec(ctx context.Context) (sql.Result, error) {
	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}
	return b.executor.ExecContext(ctx, query, args...)
}

// One executes the insertion with a RETURNING clause and unmarshals the resulting entity.
func (b *InsertBuilder[T]) One(ctx context.Context) (*T, error) {
	if len(b.returningCols) == 0 {
		b.Returning("*")
	}

	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: insert returning: %w", err)
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

// All executes a batch insertion with RETURNING and unmarshals all persisted entities.
func (b *InsertBuilder[T]) All(ctx context.Context) ([]T, error) {
	if len(b.returningCols) == 0 {
		b.Returning("*")
	}

	query, args, err := b.Build()
	if err != nil {
		return nil, err
	}

	rows, err := b.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("duosql: batch insert returning: %w", err)
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

// InsertInBatches inserts a collection of model entities in chunks of batchSize
// to avoid database driver parameter limits.
func (b *InsertBuilder[T]) InsertInBatches(ctx context.Context, batchSize int) error {
	if batchSize <= 0 {
		batchSize = 100
	}
	if len(b.models) == 0 {
		return errors.New("duosql: no models to insert in batches")
	}

	for i := 0; i < len(b.models); i += batchSize {
		end := min(i+batchSize, len(b.models))
		chunk := b.models[i:end]

		chunkBuilder := *b
		chunkBuilder.models = chunk
		_, err := chunkBuilder.Exec(ctx)
		if err != nil {
			return fmt.Errorf("duosql: insert batch %d..%d: %w", i, end, err)
		}
	}
	return nil
}
