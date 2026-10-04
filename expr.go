package duosql

import (
	"fmt"
	"strings"
)

// BuildContext tracks compilation state during query rendering, including
// the target dialect and sequential parameter counter for placeholder mapping.
type BuildContext struct {
	Dialect Dialect
	argIdx  int
}

// NewBuildContext initializes a fresh compilation context for the given dialect.
func NewBuildContext(d Dialect) *BuildContext {
	return &BuildContext{
		Dialect: d,
		argIdx:  0,
	}
}

// NextPlaceholder advances the parameter counter and returns the dialect-appropriate token.
func (bc *BuildContext) NextPlaceholder() string {
	bc.argIdx++
	return bc.Dialect.Placeholder(bc.argIdx)
}

// ResetParamCounter resets the parameter index when compiling separate query components.
func (bc *BuildContext) ResetParamCounter() {
	bc.argIdx = 0
}

// Predicate represents a composable boolean SQL condition that compiles
// into a parameterized clause and slice of execution arguments.
type Predicate interface {
	ToSQL(ctx *BuildContext) (string, []any, error)
}

// OrderByExpr represents a sort criteria expression in an ORDER BY clause.
type OrderByExpr interface {
	ToOrderBySQL(ctx *BuildContext) string
}

// binaryPredicate implements standard two-operand comparisons (=, !=, >, >=, <, <=).
type binaryPredicate struct {
	column string
	op     string
	value  any
}

func (p *binaryPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	ph := ctx.NextPlaceholder()
	col := ctx.Dialect.QuoteIdentifier(p.column)
	return fmt.Sprintf("%s %s %s", col, p.op, ph), []any{p.value}, nil
}

// Eq builds an equality predicate: col = val.
func Eq(column string, value any) Predicate {
	return &binaryPredicate{column: column, op: "=", value: value}
}

// Neq builds an inequality predicate: col != val.
func Neq(column string, value any) Predicate {
	return &binaryPredicate{column: column, op: "!=", value: value}
}

// Gt builds a greater-than predicate: col > val.
func Gt(column string, value any) Predicate {
	return &binaryPredicate{column: column, op: ">", value: value}
}

// Gte builds a greater-than-or-equal predicate: col >= val.
func Gte(column string, value any) Predicate {
	return &binaryPredicate{column: column, op: ">=", value: value}
}

// Lt builds a less-than predicate: col < val.
func Lt(column string, value any) Predicate {
	return &binaryPredicate{column: column, op: "<", value: value}
}

// Lte builds a less-than-or-equal predicate: col <= val.
func Lte(column string, value any) Predicate {
	return &binaryPredicate{column: column, op: "<=", value: value}
}

// inPredicate handles IN and NOT IN conditions over variable argument slices.
type inPredicate struct {
	column string
	values []any
	not    bool
}

func (p *inPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	if len(p.values) == 0 {
		// Standard SQL: col IN () is invalid syntax. An empty set is always false (or true for NOT IN).
		if p.not {
			return "1 = 1", nil, nil
		}
		return "1 = 0", nil, nil
	}

	var placeholders []string
	for range p.values {
		placeholders = append(placeholders, ctx.NextPlaceholder())
	}

	col := ctx.Dialect.QuoteIdentifier(p.column)
	op := "IN"
	if p.not {
		op = "NOT IN"
	}
	clause := fmt.Sprintf("%s %s (%s)", col, op, strings.Join(placeholders, ", "))
	return clause, p.values, nil
}

// In builds a membership predicate: col IN (...).
func In(column string, values ...any) Predicate {
	return &inPredicate{column: column, values: flattenValues(values), not: false}
}

// NotIn builds a non-membership predicate: col NOT IN (...).
func NotIn(column string, values ...any) Predicate {
	return &inPredicate{column: column, values: flattenValues(values), not: true}
}

// flattenValues unrolls any nested slices so callers can pass either In("id", 1, 2) or In("id", []int{1, 2}).
func flattenValues(vals []any) []any {
	var result []any
	for _, v := range vals {
		switch slice := v.(type) {
		case []any:
			result = append(result, slice...)
		case []int:
			for _, item := range slice {
				result = append(result, item)
			}
		case []int64:
			for _, item := range slice {
				result = append(result, item)
			}
		case []string:
			for _, item := range slice {
				result = append(result, item)
			}
		default:
			result = append(result, v)
		}
	}
	return result
}

// likePredicate handles LIKE and case-insensitive ILIKE patterns.
type likePredicate struct {
	column  string
	pattern string
	ilike   bool
}

func (p *likePredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	col := ctx.Dialect.QuoteIdentifier(p.column)
	ph := ctx.NextPlaceholder()
	if p.ilike {
		return ctx.Dialect.FormatILike(col, ph), []any{p.pattern}, nil
	}
	return fmt.Sprintf("%s LIKE %s", col, ph), []any{p.pattern}, nil
}

// Like builds a pattern match predicate: col LIKE pattern.
func Like(column string, pattern string) Predicate {
	return &likePredicate{column: column, pattern: pattern, ilike: false}
}

// ILike builds a case-insensitive pattern match predicate, translated cleanly
// to ILIKE in PostgreSQL and COLLATE NOCASE in SQLite.
func ILike(column string, pattern string) Predicate {
	return &likePredicate{column: column, pattern: pattern, ilike: true}
}

// nullPredicate checks for IS NULL or IS NOT NULL.
type nullPredicate struct {
	column string
	not    bool
}

func (p *nullPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	col := ctx.Dialect.QuoteIdentifier(p.column)
	if p.not {
		return fmt.Sprintf("%s IS NOT NULL", col), nil, nil
	}
	return fmt.Sprintf("%s IS NULL", col), nil, nil
}

// IsNull builds an IS NULL condition.
func IsNull(column string) Predicate {
	return &nullPredicate{column: column, not: false}
}

// IsNotNull builds an IS NOT NULL condition.
func IsNotNull(column string) Predicate {
	return &nullPredicate{column: column, not: true}
}

// betweenPredicate checks if a column is between two boundary values.
type betweenPredicate struct {
	column string
	lower  any
	upper  any
}

func (p *betweenPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	col := ctx.Dialect.QuoteIdentifier(p.column)
	ph1 := ctx.NextPlaceholder()
	ph2 := ctx.NextPlaceholder()
	return fmt.Sprintf("%s BETWEEN %s AND %s", col, ph1, ph2), []any{p.lower, p.upper}, nil
}

// Between builds a BETWEEN condition: col BETWEEN lower AND upper.
func Between(column string, lower, upper any) Predicate {
	return &betweenPredicate{column: column, lower: lower, upper: upper}
}

// compoundPredicate groups multiple predicates with logical AND / OR operators.
type compoundPredicate struct {
	preds []Predicate
	op    string
}

func (p *compoundPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	if len(p.preds) == 0 {
		return "", nil, nil
	}
	if len(p.preds) == 1 {
		return p.preds[0].ToSQL(ctx)
	}

	var parts []string
	var allArgs []any
	for _, sub := range p.preds {
		sqlPart, args, err := sub.ToSQL(ctx)
		if err != nil {
			return "", nil, err
		}
		if sqlPart != "" {
			parts = append(parts, sqlPart)
			allArgs = append(allArgs, args...)
		}
	}

	if len(parts) == 0 {
		return "", nil, nil
	}
	separator := fmt.Sprintf(" %s ", p.op)
	return fmt.Sprintf("(%s)", strings.Join(parts, separator)), allArgs, nil
}

// And combines multiple predicates with logical AND.
func And(preds ...Predicate) Predicate {
	return &compoundPredicate{preds: preds, op: "AND"}
}

// Or combines multiple predicates with logical OR.
func Or(preds ...Predicate) Predicate {
	return &compoundPredicate{preds: preds, op: "OR"}
}

// notPredicate negates an inner predicate with NOT (...).
type notPredicate struct {
	inner Predicate
}

func (p *notPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	sqlPart, args, err := p.inner.ToSQL(ctx)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("NOT (%s)", sqlPart), args, nil
}

// Not negates the given predicate.
func Not(pred Predicate) Predicate {
	return &notPredicate{inner: pred}
}

// RawExpr represents an unescaped SQL fragment with associated parameters.
type RawExpr struct {
	SQL  string
	Args []any
}

// Raw creates an escape hatch for custom SQL expressions and dialect-specific functions.
func Raw(sql string, args ...any) *RawExpr {
	return &RawExpr{SQL: sql, Args: args}
}

func (r *RawExpr) ToSQL(ctx *BuildContext) (string, []any, error) {
	// Re-map question marks to positional placeholders if running on PostgreSQL.
	if ctx.Dialect.Kind() == DialectPostgres && strings.Contains(r.SQL, "?") {
		var b strings.Builder
		for _, ch := range r.SQL {
			if ch == '?' {
				b.WriteString(ctx.NextPlaceholder())
			} else {
				b.WriteRune(ch)
			}
		}
		return b.String(), r.Args, nil
	}

	// For SQLite or queries without ?, increment placeholders if dialect is postgres and tokens were static
	return r.SQL, r.Args, nil
}

// ColumnExpr provides fluent predicate chaining on a target column name.
type ColumnExpr struct {
	name string
}

// Col begins fluent condition construction for a specific column.
func Col(name string) *ColumnExpr {
	return &ColumnExpr{name: name}
}

func (c *ColumnExpr) Eq(val any) Predicate {
	return Eq(c.name, val)
}

func (c *ColumnExpr) Neq(val any) Predicate {
	return Neq(c.name, val)
}

func (c *ColumnExpr) Gt(val any) Predicate {
	return Gt(c.name, val)
}

func (c *ColumnExpr) Gte(val any) Predicate {
	return Gte(c.name, val)
}

func (c *ColumnExpr) Lt(val any) Predicate {
	return Lt(c.name, val)
}

func (c *ColumnExpr) Lte(val any) Predicate {
	return Lte(c.name, val)
}

func (c *ColumnExpr) In(vals ...any) Predicate {
	return In(c.name, vals...)
}

func (c *ColumnExpr) NotIn(vals ...any) Predicate {
	return NotIn(c.name, vals...)
}

func (c *ColumnExpr) Like(pattern string) Predicate {
	return Like(c.name, pattern)
}

func (c *ColumnExpr) ILike(pattern string) Predicate {
	return ILike(c.name, pattern)
}

func (c *ColumnExpr) IsNull() Predicate {
	return IsNull(c.name)
}

func (c *ColumnExpr) IsNotNull() Predicate {
	return IsNotNull(c.name)
}

func (c *ColumnExpr) Between(min, max any) Predicate {
	return Between(c.name, min, max)
}

func (c *ColumnExpr) Asc() OrderByExpr {
	return Asc(c.name)
}

func (c *ColumnExpr) Desc() OrderByExpr {
	return Desc(c.name)
}

// orderByClause implements OrderByExpr.
type orderByClause struct {
	column    string
	direction string
}

func (o *orderByClause) ToOrderBySQL(ctx *BuildContext) string {
	return fmt.Sprintf("%s %s", ctx.Dialect.QuoteIdentifier(o.column), o.direction)
}

// Asc orders results in ascending order.
func Asc(column string) OrderByExpr {
	return &orderByClause{column: column, direction: "ASC"}
}

// Desc orders results in descending order.
func Desc(column string) OrderByExpr {
	return &orderByClause{column: column, direction: "DESC"}
}

// rawOrderBy implements OrderByExpr with an arbitrary SQL expression.
type rawOrderBy struct {
	expr string
}

func (r *rawOrderBy) ToOrderBySQL(ctx *BuildContext) string {
	return r.expr
}

// RawOrderBy creates an OrderByExpr from a raw SQL expression (such as CASE statements).
func RawOrderBy(expr string) OrderByExpr {
	return &rawOrderBy{expr: expr}
}

// SubqueryBuilder compiles an executable subquery fragment.
type SubqueryBuilder interface {
	Build() (string, []any, error)
}

// subqueryPredicate represents conditions evaluated against subquery results.
type subqueryPredicate struct {
	column   string
	sub      SubqueryBuilder
	not      bool
	isExists bool
}

func (p *subqueryPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	subSQL, subArgs, err := p.sub.Build()
	if err != nil {
		return "", nil, fmt.Errorf("duosql: build subquery: %w", err)
	}

	finalSQL := subSQL
	if ctx.Dialect.Kind() == DialectPostgres && len(subArgs) > 0 {
		finalSQL = renumberPostgresTokens(subSQL, len(subArgs), ctx)
	} else if ctx.Dialect.Kind() == DialectSQLite {
		for range subArgs {
			ctx.NextPlaceholder()
		}
	}

	if p.isExists {
		if p.not {
			return fmt.Sprintf("NOT EXISTS (%s)", finalSQL), subArgs, nil
		}
		return fmt.Sprintf("EXISTS (%s)", finalSQL), subArgs, nil
	}

	col := ctx.Dialect.QuoteIdentifier(p.column)
	op := "IN"
	if p.not {
		op = "NOT IN"
	}
	return fmt.Sprintf("%s %s (%s)", col, op, finalSQL), subArgs, nil
}

func renumberPostgresTokens(sqlStr string, count int, ctx *BuildContext) string {
	// Re-map internal sequential tokens $1..$N to the outer context's sequential counter
	res := sqlStr
	for i := 1; i <= count; i++ {
		oldToken := fmt.Sprintf("$%d", i)
		newToken := ctx.NextPlaceholder()
		// Replace only whole word tokens
		res = replaceTokenWord(res, oldToken, newToken)
	}
	return res
}

func replaceTokenWord(s, oldToken, newToken string) string {
	var b strings.Builder
	idx := 0
	for {
		pos := strings.Index(s[idx:], oldToken)
		if pos == -1 {
			b.WriteString(s[idx:])
			break
		}
		matchPos := idx + pos
		endPos := matchPos + len(oldToken)
		// Check that token is not part of a larger number (e.g. $1 vs $10)
		isBoundary := endPos >= len(s) || s[endPos] < '0' || s[endPos] > '9'
		if isBoundary {
			b.WriteString(s[idx:matchPos])
			b.WriteString(newToken)
			idx = endPos
		} else {
			b.WriteString(s[idx : matchPos+1])
			idx = matchPos + 1
		}
	}
	return b.String()
}

// InSubquery tests if a column value matches any record returned by the subquery.
func InSubquery(column string, sub SubqueryBuilder) Predicate {
	return &subqueryPredicate{column: column, sub: sub, not: false, isExists: false}
}

// NotInSubquery tests if a column value does not match any record returned by the subquery.
func NotInSubquery(column string, sub SubqueryBuilder) Predicate {
	return &subqueryPredicate{column: column, sub: sub, not: true, isExists: false}
}

// ExistsSubquery tests if the subquery returns at least one record.
func ExistsSubquery(sub SubqueryBuilder) Predicate {
	return &subqueryPredicate{sub: sub, not: false, isExists: true}
}

// NotExistsSubquery tests if the subquery returns zero records.
func NotExistsSubquery(sub SubqueryBuilder) Predicate {
	return &subqueryPredicate{sub: sub, not: true, isExists: true}
}

// JSONExtractExpr provides JSON path extraction predicates.
type JSONExtractExpr struct {
	column string
	path   string
}

// JSONExtract initiates fluent JSON field extraction.
func JSONExtract(column string, path string) *JSONExtractExpr {
	return &JSONExtractExpr{column: column, path: path}
}

type jsonBinaryPredicate struct {
	expr  *JSONExtractExpr
	op    string
	value any
}

func (p *jsonBinaryPredicate) ToSQL(ctx *BuildContext) (string, []any, error) {
	colSQL := ctx.Dialect.JSONExtractSQL(p.expr.column, p.expr.path)
	ph := ctx.NextPlaceholder()
	return fmt.Sprintf("%s %s %s", colSQL, p.op, ph), []any{p.value}, nil
}

// Eq matches extracted JSON value to equal the target value.
func (j *JSONExtractExpr) Eq(val any) Predicate {
	return &jsonBinaryPredicate{expr: j, op: "=", value: val}
}

// Neq matches extracted JSON value to not equal the target value.
func (j *JSONExtractExpr) Neq(val any) Predicate {
	return &jsonBinaryPredicate{expr: j, op: "!=", value: val}
}

// Like evaluates a LIKE pattern against the extracted JSON value.
func (j *JSONExtractExpr) Like(pattern string) Predicate {
	return &jsonBinaryPredicate{expr: j, op: "LIKE", value: pattern}
}
