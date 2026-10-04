package duosql

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// FieldInfo stores parsed struct field metadata cached across query executions.
type FieldInfo struct {
	Index         int
	Name          string
	ColumnName    string
	IsPrimaryKey  bool
	IsAuto        bool
	IsJSON        bool
	IsProto       bool
	IsSoftDelete  bool
	IsCreatedAt   bool
	IsUpdatedAt   bool
	IsIndex       bool
	IsUniqueIndex bool
	IndexName     string
	FieldType     reflect.Type
}

// ModelIndex specifies an index declared via struct tags.
type ModelIndex struct {
	Name    string
	Columns []string
	Unique  bool
}

// ModelMetadata represents the cached structural blueprint of a Go entity struct.
type ModelMetadata struct {
	Type             reflect.Type
	TableName        string
	Fields           []FieldInfo
	ColumnToIdx      map[string]int
	PKColumn         string
	SoftDeleteColumn string
	CreatedAtCol     string
	UpdatedAtCol     string
	Indexes          []ModelIndex
}

var modelCache sync.Map // map[reflect.Type]*ModelMetadata

// GetModelMetadata inspects type T and retrieves cached reflection metadata.
func GetModelMetadata[T any]() (*ModelMetadata, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		return nil, fmt.Errorf("duosql: cannot inspect nil interface type")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("duosql: model type must be a struct, received %s", t.Kind())
	}

	if val, ok := modelCache.Load(t); ok {
		return val.(*ModelMetadata), nil
	}

	meta := parseStructMetadata(t)
	modelCache.Store(t, meta)
	return meta, nil
}

func parseStructMetadata(t reflect.Type) *ModelMetadata {
	tableName := toSnakeCase(t.Name()) + "s"
	zero := reflect.New(t).Interface()
	if namer, ok := zero.(interface{ TableName() string }); ok {
		tableName = namer.TableName()
	}

	meta := &ModelMetadata{
		Type:        t,
		TableName:   tableName,
		ColumnToIdx: make(map[string]int),
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		info := parseFieldInfo(i, field)
		if info.ColumnName == "-" {
			continue
		}

		meta.Fields = append(meta.Fields, info)
		meta.ColumnToIdx[info.ColumnName] = len(meta.Fields) - 1
		assignSpecialColumns(meta, info)
	}

	applyIdConvention(meta)
	return meta
}

func assignSpecialColumns(meta *ModelMetadata, info FieldInfo) {
	if info.IsPrimaryKey && meta.PKColumn == "" {
		meta.PKColumn = info.ColumnName
	}
	if info.IsSoftDelete && meta.SoftDeleteColumn == "" {
		meta.SoftDeleteColumn = info.ColumnName
	}
	if info.IsCreatedAt && meta.CreatedAtCol == "" {
		meta.CreatedAtCol = info.ColumnName
	}
	if info.IsUpdatedAt && meta.UpdatedAtCol == "" {
		meta.UpdatedAtCol = info.ColumnName
	}
	if info.IsIndex {
		idxName := info.IndexName
		if idxName == "" {
			prefix := "idx"
			if info.IsUniqueIndex {
				prefix = "uniq"
			}
			idxName = fmt.Sprintf("%s_%s_%s", prefix, meta.TableName, info.ColumnName)
		}
		meta.Indexes = append(meta.Indexes, ModelIndex{
			Name:    idxName,
			Columns: []string{info.ColumnName},
			Unique:  info.IsUniqueIndex,
		})
	}
}

func applyIdConvention(meta *ModelMetadata) {
	if meta.PKColumn == "" && len(meta.Fields) > 0 {
		if idx, exists := meta.ColumnToIdx["id"]; exists {
			meta.Fields[idx].IsPrimaryKey = true
			meta.PKColumn = "id"
		}
	}
}

func parseFieldInfo(idx int, field reflect.StructField) FieldInfo {
	tag := field.Tag.Get("duo")
	if tag == "" {
		tag = field.Tag.Get("db")
	}

	info := parseTagOptions(tag, field.Name)
	info.Index = idx
	info.Name = field.Name
	info.FieldType = field.Type
	if isProtoType(field.Type) {
		info.IsProto = true
	}
	return info
}

func parseTagOptions(tag, fieldName string) FieldInfo {
	var info FieldInfo
	if tag != "" {
		parts := strings.Split(tag, ",")
		info.ColumnName = strings.TrimSpace(parts[0])
		for _, opt := range parts[1:] {
			applyTagOption(strings.TrimSpace(opt), &info)
		}
	}

	if info.ColumnName == "" {
		info.ColumnName = toSnakeCase(fieldName)
	}

	lowerCol := strings.ToLower(info.ColumnName)
	if lowerCol == "created_at" || strings.EqualFold(fieldName, "CreatedAt") {
		info.IsCreatedAt = true
	}
	if lowerCol == "updated_at" || strings.EqualFold(fieldName, "UpdatedAt") {
		info.IsUpdatedAt = true
	}

	return info
}

func applyTagOption(opt string, info *FieldInfo) {
	lower := strings.ToLower(opt)
	if applyIndexTagOption(opt, lower, info) {
		return
	}
	switch lower {
	case "pk":
		info.IsPrimaryKey = true
	case "auto":
		info.IsAuto = true
	case "json":
		info.IsJSON = true
	case "proto":
		info.IsProto = true
	case "soft_delete", "softdelete":
		info.IsSoftDelete = true
	case "created_at", "createdat", "auto_now_add":
		info.IsCreatedAt = true
	case "updated_at", "updatedat", "auto_now":
		info.IsUpdatedAt = true
	}
}

func applyIndexTagOption(opt, lower string, info *FieldInfo) bool {
	switch {
	case lower == "unique" || lower == "unique_index":
		info.IsUniqueIndex = true
		info.IsIndex = true
		return true
	case strings.HasPrefix(lower, "unique:"):
		info.IsUniqueIndex = true
		info.IsIndex = true
		info.IndexName = opt[7:]
		return true
	case lower == "index":
		info.IsIndex = true
		return true
	case strings.HasPrefix(lower, "index:"):
		info.IsIndex = true
		info.IndexName = opt[6:]
		return true
	default:
		return false
	}
}

// ColumnNames returns all declared column names in the model.
func (m *ModelMetadata) ColumnNames() []string {
	cols := make([]string, len(m.Fields))
	for i, f := range m.Fields {
		cols[i] = f.ColumnName
	}
	return cols
}

// ExtractInsertMap converts a model instance into a map of column names to values,
// optionally skipping auto-generated columns and automatically populating timestamps.
func (m *ModelMetadata) ExtractInsertMap(val reflect.Value, skipAuto bool) map[string]any {
	for val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	data := make(map[string]any, len(m.Fields))
	now := time.Now().UTC()

	for _, f := range m.Fields {
		if skipAuto && f.IsAuto {
			continue
		}
		fieldVal := val.Field(f.Index)

		if f.IsCreatedAt {
			if applyTimestamp(fieldVal, now, data, f.ColumnName) {
				continue
			}
		}

		if f.IsUpdatedAt {
			if applyTimestamp(fieldVal, now, data, f.ColumnName) {
				continue
			}
		}

		if f.IsJSON {
			bytes, err := json.Marshal(fieldVal.Interface())
			if err == nil {
				data[f.ColumnName] = string(bytes)
				continue
			}
		}

		if f.IsProto {
			if fieldVal.Kind() == reflect.Pointer && fieldVal.IsNil() {
				data[f.ColumnName] = nil
				continue
			}
			bytes, err := marshalProto(fieldVal.Interface())
			if err == nil {
				data[f.ColumnName] = bytes
				continue
			}
		}

		data[f.ColumnName] = fieldVal.Interface()
	}
	return data
}

func applyTimestamp(fieldVal reflect.Value, now time.Time, data map[string]any, colName string) bool {
	if fieldVal.Type() == reflect.TypeFor[time.Time]() {
		tVal := fieldVal.Interface().(time.Time)
		if tVal.IsZero() {
			if fieldVal.CanSet() {
				fieldVal.Set(reflect.ValueOf(now))
			}
			data[colName] = now
			return true
		}
	} else if fieldVal.Kind() == reflect.Int64 && fieldVal.Int() == 0 {
		u := now.Unix()
		if fieldVal.CanSet() {
			fieldVal.SetInt(u)
		}
		data[colName] = u
		return true
	}
	return false
}

// ScanTargets prepares scan destination pointers for the SQL driver's Rows.Scan
// matching the specific columns returned by the executed query.
func (m *ModelMetadata) ScanTargets(val reflect.Value, columns []string) ([]any, []jsonScannerTarget, []protoScannerTarget) {
	for val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	targets := make([]any, len(columns))
	var jsonTargets []jsonScannerTarget
	var protoTargets []protoScannerTarget

	for i, col := range columns {
		idx, found := m.ColumnToIdx[col]
		if !found {
			// Ignore unmapped database columns gracefully by scanning into a discard receptacle.
			var dummy any
			targets[i] = &dummy
			continue
		}

		field := m.Fields[idx]
		fieldVal := val.Field(field.Index)

		if field.IsJSON {
			var raw []byte
			targets[i] = &raw
			jsonTargets = append(jsonTargets, jsonScannerTarget{
				fieldVal: fieldVal,
				rawBytes: &raw,
			})
			continue
		}

		if field.IsProto {
			var raw []byte
			targets[i] = &raw
			protoTargets = append(protoTargets, protoScannerTarget{
				fieldVal: fieldVal,
				rawBytes: &raw,
			})
			continue
		}

		if field.FieldType == timeType {
			targets[i] = &timeScanner{dest: fieldVal.Addr().Interface().(*time.Time)}
			continue
		}
		if field.FieldType == timePtrType {
			targets[i] = &timePtrScanner{dest: fieldVal.Addr().Interface().(**time.Time)}
			continue
		}

		targets[i] = fieldVal.Addr().Interface()
	}

	return targets, jsonTargets, protoTargets
}

var (
	timeType    = reflect.TypeFor[time.Time]()
	timePtrType = reflect.TypeFor[*time.Time]()
)

type timeScanner struct {
	dest *time.Time
}

func (s *timeScanner) Scan(src any) error {
	if src == nil {
		*s.dest = time.Time{}
		return nil
	}
	t, err := parseAnyTime(src)
	if err != nil {
		return err
	}
	*s.dest = t
	return nil
}

type timePtrScanner struct {
	dest **time.Time
}

func (s *timePtrScanner) Scan(src any) error {
	if src == nil {
		*s.dest = nil
		return nil
	}
	t, err := parseAnyTime(src)
	if err != nil {
		return err
	}
	*s.dest = &t
	return nil
}

func parseAnyTime(src any) (time.Time, error) {
	switch v := src.(type) {
	case time.Time:
		return v, nil
	case string:
		return parseTimeString(v)
	case []byte:
		return parseTimeString(string(v))
	case int64:
		return time.Unix(v, 0), nil
	default:
		return time.Time{}, fmt.Errorf("duosql: unsupported time scan type %T", src)
	}
}

func parseTimeString(s string) (time.Time, error) {
	if idx := strings.Index(s, " m="); idx != -1 {
		s = s[:idx]
	}

	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05 -0700",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("duosql: cannot parse %q as time.Time", s)
}

type jsonScannerTarget struct {
	fieldVal reflect.Value
	rawBytes *[]byte
}

func (j *jsonScannerTarget) apply() error {
	if j.rawBytes == nil || len(*j.rawBytes) == 0 {
		return nil
	}
	dest := reflect.New(j.fieldVal.Type()).Interface()
	if err := json.Unmarshal(*j.rawBytes, dest); err != nil {
		return fmt.Errorf("duosql: json unmarshal: %w", err)
	}
	j.fieldVal.Set(reflect.ValueOf(dest).Elem())
	return nil
}

type protoScannerTarget struct {
	fieldVal reflect.Value
	rawBytes *[]byte
}

func (p *protoScannerTarget) apply() error {
	if p.rawBytes == nil || len(*p.rawBytes) == 0 {
		return nil
	}
	return unmarshalProto(*p.rawBytes, p.fieldVal)
}

// ScanModel scans a single row from *sql.Rows into a fresh instance of T.
func ScanModel[T any](rows *sql.Rows, columns []string, meta *ModelMetadata) (*T, error) {
	item := new(T)
	val := reflect.ValueOf(item).Elem()

	targets, jsonTargets, protoTargets := meta.ScanTargets(val, columns)
	if err := rows.Scan(targets...); err != nil {
		return nil, fmt.Errorf("duosql: scan row: %w", err)
	}

	for _, jt := range jsonTargets {
		if err := jt.apply(); err != nil {
			return nil, err
		}
	}

	for _, pt := range protoTargets {
		if err := pt.apply(); err != nil {
			return nil, err
		}
	}

	return item, nil
}

func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
