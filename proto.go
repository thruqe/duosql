package duosql

import (
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"
)

var protoMessageType = reflect.TypeFor[proto.Message]()

// isProtoType reports whether the specified reflection type implements proto.Message.
func isProtoType(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if t.Implements(protoMessageType) {
		return true
	}
	if t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(protoMessageType) {
		return true
	}
	return false
}

// marshalProto encodes a protobuf message into binary wire format for database storage.
func marshalProto(val any) ([]byte, error) {
	if val == nil {
		return nil, nil
	}
	pm, ok := val.(proto.Message)
	if !ok {
		// Attempt pointer conversion if val is a value struct
		rv := reflect.ValueOf(val)
		if rv.CanAddr() {
			if pmAddr, ok := rv.Addr().Interface().(proto.Message); ok {
				return proto.Marshal(pmAddr)
			}
		}
		return nil, fmt.Errorf("duosql: value of type %T does not implement proto.Message", val)
	}
	return proto.Marshal(pm)
}

// unmarshalProto decodes binary protobuf wire data into destination reflection pointer.
func unmarshalProto(data []byte, dest reflect.Value) error {
	if len(data) == 0 {
		return nil
	}

	for dest.Kind() == reflect.Pointer && dest.Type() != reflect.PointerTo(dest.Type().Elem()) {
		if dest.IsNil() {
			dest.Set(reflect.New(dest.Type().Elem()))
		}
		dest = dest.Elem()
	}

	t := dest.Type()
	if t.Kind() == reflect.Pointer {
		elemType := t.Elem()
		newObj := reflect.New(elemType)
		if pm, ok := newObj.Interface().(proto.Message); ok {
			if err := proto.Unmarshal(data, pm); err != nil {
				return fmt.Errorf("duosql: unmarshal protobuf: %w", err)
			}
			dest.Set(newObj)
			return nil
		}
	}

	return fmt.Errorf("duosql: cannot unmarshal protobuf into %s", t)
}
