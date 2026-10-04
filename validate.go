package duosql

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// ModelValidator can be implemented by models to run custom programmatic validations.
type ModelValidator interface {
	Validate() error
}

// ModelValidatorWithContext allows models to perform context-aware validations (e.g. uniqueness checks).
type ModelValidatorWithContext interface {
	ValidateContext(ctx context.Context) error
}

// ValidationError represents an individual rule violation on a specific struct field.
type ValidationError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}

// Error formats the validation failure into a human-readable string.
func (e ValidationError) Error() string {
	return e.Message
}

// ValidationErrors collects all failures encountered across a model.
type ValidationErrors []ValidationError

// Error concatenates all error messages into a single string.
func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return ""
	}
	var msgs []string
	for _, e := range ve {
		msgs = append(msgs, e.Message)
	}
	return strings.Join(msgs, "; ")
}

// FieldErrors returns a mapping of field names to their failure messages for direct API responses.
func (ve ValidationErrors) FieldErrors() map[string]string {
	m := make(map[string]string, len(ve))
	for _, e := range ve {
		m[e.Field] = e.Message
	}
	return m
}

var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	uuidRegex  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Validate inspects a struct instance, evaluating tag-based rules and custom validator interfaces.
func Validate(v any) error {
	return ValidateContext(context.Background(), v)
}

// ValidateContext validates a struct with context propagation for custom contextual hooks.
func ValidateContext(ctx context.Context, v any) error {
	if v == nil {
		return nil
	}

	val := reflect.ValueOf(v)
	for val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil
	}

	var errs ValidationErrors
	t := val.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		rules := extractValidationRules(field)
		if len(rules) == 0 {
			continue
		}

		fVal := val.Field(i)
		fieldName := resolveValidationFieldName(field)
		fieldErrs := validateFieldValue(fieldName, fVal, rules)
		errs = append(errs, fieldErrs...)
	}

	if len(errs) > 0 {
		return errs
	}

	return runModelValidatorHooks(ctx, v)
}

func extractValidationRules(field reflect.StructField) []string {
	tag := field.Tag.Get("validate")
	if tag != "" {
		return strings.Split(tag, ",")
	}
	duoTag := field.Tag.Get("duo")
	if duoTag == "" {
		return nil
	}
	for part := range strings.SplitSeq(duoTag, ",") {
		part = strings.TrimSpace(part)
		if after, ok := strings.CutPrefix(part, "validate="); ok {
			return strings.Split(after, ";")
		}
	}
	return nil
}

func resolveValidationFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag != "" && tag != "-" {
		parts := strings.Split(tag, ",")
		if parts[0] != "" {
			return parts[0]
		}
	}
	duo := field.Tag.Get("duo")
	if duo != "" && duo != "-" {
		parts := strings.Split(duo, ",")
		if parts[0] != "" {
			return parts[0]
		}
	}
	return toSnakeCase(field.Name)
}

func runModelValidatorHooks(ctx context.Context, v any) error {
	if cv, ok := v.(ModelValidatorWithContext); ok {
		if err := cv.ValidateContext(ctx); err != nil {
			return err
		}
	}
	if mv, ok := v.(ModelValidator); ok {
		return mv.Validate()
	}
	return nil
}

func validateFieldValue(fieldName string, val reflect.Value, rules []string) []ValidationError {
	var errs []ValidationError

	for _, ruleDef := range rules {
		ruleDef = strings.TrimSpace(ruleDef)
		if ruleDef == "" {
			continue
		}

		ruleName, param := parseRuleDef(ruleDef)
		if err := evaluateRule(fieldName, val, ruleName, param); err != nil {
			errs = append(errs, *err)
		}
	}

	return errs
}

func parseRuleDef(def string) (string, string) {
	if before, after, ok := strings.Cut(def, "="); ok {
		return before, after
	}
	return def, ""
}

type ruleChecker func(fieldName string, val reflect.Value, param string) *ValidationError

var ruleCheckers = map[string]ruleChecker{
	"required": func(f string, v reflect.Value, _ string) *ValidationError { return checkRequired(f, v) },
	"min":      checkMin,
	"max":      checkMax,
	"len":      checkLen,
	"email":    func(f string, v reflect.Value, _ string) *ValidationError { return checkEmail(f, v) },
	"url":      func(f string, v reflect.Value, _ string) *ValidationError { return checkURL(f, v) },
	"uuid":     func(f string, v reflect.Value, _ string) *ValidationError { return checkUUID(f, v) },
	"alpha":    func(f string, v reflect.Value, _ string) *ValidationError { return checkAlpha(f, v) },
	"alphanum": func(f string, v reflect.Value, _ string) *ValidationError { return checkAlphanum(f, v) },
	"numeric":  func(f string, v reflect.Value, _ string) *ValidationError { return checkNumeric(f, v) },
	"in":       checkIn,
	"oneof":    checkIn,
	"not_in":   checkNotIn,
	"regex":    checkRegex,
}

func evaluateRule(fieldName string, val reflect.Value, rule, param string) *ValidationError {
	if checker, ok := ruleCheckers[rule]; ok {
		return checker(fieldName, val, param)
	}
	return nil
}

func checkRequired(fieldName string, val reflect.Value) *ValidationError {
	if isZeroOrEmpty(val) {
		return &ValidationError{
			Field:   fieldName,
			Rule:    "required",
			Message: fmt.Sprintf("field '%s' is required", fieldName),
		}
	}
	return nil
}

func isZeroOrEmpty(val reflect.Value) bool {
	if !val.IsValid() {
		return true
	}
	switch val.Kind() {
	case reflect.String:
		return strings.TrimSpace(val.String()) == ""
	case reflect.Slice, reflect.Map:
		return val.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return val.IsNil()
	default:
		return val.IsZero()
	}
}

func checkMin(fieldName string, val reflect.Value, param string) *ValidationError {
	threshold, err := strconv.ParseFloat(param, 64)
	if err != nil {
		return nil
	}
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if float64(val.Int()) < threshold {
			return &ValidationError{Field: fieldName, Rule: "min", Param: param, Message: fmt.Sprintf("field '%s' must be at least %s", fieldName, param)}
		}
	case reflect.Float32, reflect.Float64:
		if val.Float() < threshold {
			return &ValidationError{Field: fieldName, Rule: "min", Param: param, Message: fmt.Sprintf("field '%s' must be at least %s", fieldName, param)}
		}
	case reflect.String:
		if float64(len(val.String())) < threshold {
			return &ValidationError{Field: fieldName, Rule: "min", Param: param, Message: fmt.Sprintf("field '%s' length must be at least %s characters", fieldName, param)}
		}
	case reflect.Slice, reflect.Map:
		if float64(val.Len()) < threshold {
			return &ValidationError{Field: fieldName, Rule: "min", Param: param, Message: fmt.Sprintf("field '%s' must contain at least %s items", fieldName, param)}
		}
	}
	return nil
}

func checkMax(fieldName string, val reflect.Value, param string) *ValidationError {
	threshold, err := strconv.ParseFloat(param, 64)
	if err != nil {
		return nil
	}
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if float64(val.Int()) > threshold {
			return &ValidationError{Field: fieldName, Rule: "max", Param: param, Message: fmt.Sprintf("field '%s' must be at most %s", fieldName, param)}
		}
	case reflect.Float32, reflect.Float64:
		if val.Float() > threshold {
			return &ValidationError{Field: fieldName, Rule: "max", Param: param, Message: fmt.Sprintf("field '%s' must be at most %s", fieldName, param)}
		}
	case reflect.String:
		if float64(len(val.String())) > threshold {
			return &ValidationError{Field: fieldName, Rule: "max", Param: param, Message: fmt.Sprintf("field '%s' length must be at most %s characters", fieldName, param)}
		}
	case reflect.Slice, reflect.Map:
		if float64(val.Len()) > threshold {
			return &ValidationError{Field: fieldName, Rule: "max", Param: param, Message: fmt.Sprintf("field '%s' must contain at most %s items", fieldName, param)}
		}
	}
	return nil
}

func checkLen(fieldName string, val reflect.Value, param string) *ValidationError {
	target, err := strconv.Atoi(param)
	if err != nil {
		return nil
	}
	switch val.Kind() {
	case reflect.String:
		if len(val.String()) != target {
			return &ValidationError{Field: fieldName, Rule: "len", Param: param, Message: fmt.Sprintf("field '%s' must have length %d", fieldName, target)}
		}
	case reflect.Slice:
		if val.Len() != target {
			return &ValidationError{Field: fieldName, Rule: "len", Param: param, Message: fmt.Sprintf("field '%s' must have exactly %d items", fieldName, target)}
		}
	}
	return nil
}

func checkEmail(fieldName string, val reflect.Value) *ValidationError {
	if val.Kind() != reflect.String {
		return nil
	}
	str := strings.TrimSpace(val.String())
	if str == "" {
		return nil
	}
	if !emailRegex.MatchString(str) {
		return &ValidationError{Field: fieldName, Rule: "email", Message: fmt.Sprintf("field '%s' must be a valid email address", fieldName)}
	}
	return nil
}

func checkURL(fieldName string, val reflect.Value) *ValidationError {
	if val.Kind() != reflect.String {
		return nil
	}
	str := strings.TrimSpace(val.String())
	if str == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(str)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return &ValidationError{Field: fieldName, Rule: "url", Message: fmt.Sprintf("field '%s' must be a valid HTTP/HTTPS URL", fieldName)}
	}
	return nil
}

func checkUUID(fieldName string, val reflect.Value) *ValidationError {
	if val.Kind() != reflect.String {
		return nil
	}
	str := strings.TrimSpace(val.String())
	if str == "" {
		return nil
	}
	if !uuidRegex.MatchString(str) {
		return &ValidationError{Field: fieldName, Rule: "uuid", Message: fmt.Sprintf("field '%s' must be a valid UUID", fieldName)}
	}
	return nil
}

func checkAlpha(fieldName string, val reflect.Value) *ValidationError {
	if val.Kind() != reflect.String {
		return nil
	}
	str := val.String()
	if str == "" {
		return nil
	}
	for _, r := range str {
		if !unicode.IsLetter(r) {
			return &ValidationError{Field: fieldName, Rule: "alpha", Message: fmt.Sprintf("field '%s' must contain only alphabetic letters", fieldName)}
		}
	}
	return nil
}

func checkAlphanum(fieldName string, val reflect.Value) *ValidationError {
	if val.Kind() != reflect.String {
		return nil
	}
	str := val.String()
	if str == "" {
		return nil
	}
	for _, r := range str {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return &ValidationError{Field: fieldName, Rule: "alphanum", Message: fmt.Sprintf("field '%s' must contain only letters and digits", fieldName)}
		}
	}
	return nil
}

func checkNumeric(fieldName string, val reflect.Value) *ValidationError {
	if val.Kind() != reflect.String {
		return nil
	}
	str := val.String()
	if str == "" {
		return nil
	}
	for _, r := range str {
		if !unicode.IsDigit(r) {
			return &ValidationError{Field: fieldName, Rule: "numeric", Message: fmt.Sprintf("field '%s' must contain only digits", fieldName)}
		}
	}
	return nil
}

func checkIn(fieldName string, val reflect.Value, param string) *ValidationError {
	options := strings.Split(param, "|")
	if len(options) == 1 {
		options = strings.Fields(param)
	}
	str := fmt.Sprintf("%v", val.Interface())
	if slices.Contains(options, str) {
		return nil
	}
	return &ValidationError{
		Field:   fieldName,
		Rule:    "in",
		Param:   param,
		Message: fmt.Sprintf("field '%s' must be one of [%s]", fieldName, strings.Join(options, ", ")),
	}
}

func checkNotIn(fieldName string, val reflect.Value, param string) *ValidationError {
	options := strings.Split(param, "|")
	if len(options) == 1 {
		options = strings.Fields(param)
	}
	str := fmt.Sprintf("%v", val.Interface())
	if slices.Contains(options, str) {
		return &ValidationError{
			Field:   fieldName,
			Rule:    "not_in",
			Param:   param,
			Message: fmt.Sprintf("field '%s' cannot be one of [%s]", fieldName, strings.Join(options, ", ")),
		}
	}
	return nil
}

func checkRegex(fieldName string, val reflect.Value, pattern string) *ValidationError {
	if val.Kind() != reflect.String {
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	if !re.MatchString(val.String()) {
		return &ValidationError{
			Field:   fieldName,
			Rule:    "regex",
			Param:   pattern,
			Message: fmt.Sprintf("field '%s' does not match pattern '%s'", fieldName, pattern),
		}
	}
	return nil
}
