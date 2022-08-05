// Copyright GoFrame Author(https://goframe.org). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://github.com/gogf/gf.

package gdb

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"time"

	"github.com/gogf/gf/v2/encoding/gbinary"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/internal/json"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/gogf/gf/v2/util/gconv"
	"github.com/gogf/gf/v2/util/gutil"
)

// ConvertDataForRecord is a very important function, which does converting for any data that
// will be inserted into table/collection as a record.
//
// The parameter `value` should be type of *map/map/*struct/struct.
// It supports embedded struct definition for struct.
func (c *Core) ConvertDataForRecord(ctx context.Context, value interface{}) (map[string]interface{}, error) {
	var (
		err  error
		data = DataToMapDeep(value)
	)
	for k, v := range data {
		data[k], err = c.ConvertDataForRecordValue(ctx, v)
		if err != nil {
			return nil, gerror.Wrapf(err, `ConvertDataForRecordValue failed for value: %#v`, v)
		}
	}
	return data, nil
}

func (c *Core) ConvertDataForRecordValue(ctx context.Context, value interface{}) (interface{}, error) {
	var (
		err            error
		convertedValue = value
	)
	// If `value` implements interface `driver.Valuer`, it then uses the interface for value converting.
	if valuer, ok := value.(driver.Valuer); ok {
		if convertedValue, err = valuer.Value(); err != nil {
			if err != nil {
				return nil, err
			}
		}
		return convertedValue, nil
	}
	// Default value converting.
	var (
		rvValue = reflect.ValueOf(value)
		rvKind  = rvValue.Kind()
	)
	for rvKind == reflect.Ptr {
		rvValue = rvValue.Elem()
		rvKind = rvValue.Kind()
	}
	switch rvKind {
	case reflect.Slice, reflect.Array, reflect.Map:
		// It should ignore the bytes type.
		if _, ok := value.([]byte); !ok {
			// Convert the value to JSON.
			convertedValue, err = json.Marshal(value)
			if err != nil {
				return nil, err
			}
		}

	case reflect.Struct:
		switch r := value.(type) {
		// If the time is zero, it then updates it to nil,
		// which will insert/update the value to database as "null".
		case time.Time:
			if r.IsZero() {
				convertedValue = nil
			}

		case gtime.Time:
			if r.IsZero() {
				convertedValue = nil
			} else {
				convertedValue = r.Time
			}

		case *gtime.Time:
			if r.IsZero() {
				convertedValue = nil
			} else {
				convertedValue = r.Time
			}

		case *time.Time:
			// Nothing to do.

		case Counter, *Counter:
			// Nothing to do.

		default:
			// Use string conversion in default.
			if s, ok := value.(iString); ok {
				convertedValue = s.String()
			} else {
				// Convert the value to JSON.
				convertedValue, err = json.Marshal(value)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return convertedValue, nil
}

// ConvertValueForLocal converts value to local Golang type of value according field type name from database.
// The parameter `fieldType` is in lower case, like:
// `float(5,2)`, `unsigned double(5,2)`, `decimal(10,2)`, `char(45)`, `varchar(100)`, etc.
func (c *Core) ConvertValueForLocal(ctx context.Context, fieldType string, fieldValue interface{}) (interface{}, error) {
	// If there's no type retrieved, it returns the `fieldValue` directly
	// to use its original data type, as `fieldValue` is type of interface{}.
	if fieldType == "" {
		return fieldValue, nil
	}
	typeName, err := CheckValueForLocalType(ctx, fieldType, fieldValue)
	if err != nil {
		return nil, err
	}
	switch typeName {
	case typeBytes:
		if strings.Contains(typeName, "binary") || strings.Contains(typeName, "blob") {
			return fieldValue, nil
		}
		return gconv.Bytes(fieldValue), nil

	case typeInt:
		return gconv.Int(gconv.String(fieldValue)), nil

	case typeUint:
		return gconv.Uint(gconv.String(fieldValue)), nil

	case typeInt64:
		return gconv.Int64(gconv.String(fieldValue)), nil

	case typeUint64:
		return gconv.Uint64(gconv.String(fieldValue)), nil

	case typeInt64Bytes:
		return gbinary.BeDecodeToInt64(gconv.Bytes(fieldValue)), nil

	case typeUint64Bytes:
		return gbinary.BeDecodeToUint64(gconv.Bytes(fieldValue)), nil

	case typeFloat32:
		return gconv.Float32(gconv.String(fieldValue)), nil

	case typeFloat64:
		return gconv.Float64(gconv.String(fieldValue)), nil

	case typeBool:
		s := gconv.String(fieldValue)
		// mssql is true|false string.
		if strings.EqualFold(s, "true") {
			return 1, nil
		}
		if strings.EqualFold(s, "false") {
			return 0, nil
		}
		return gconv.Bool(fieldValue), nil

	case typeDate:
		// Date without time.
		if t, ok := fieldValue.(time.Time); ok {
			return gtime.NewFromTime(t).Format("Y-m-d"), nil
		}
		t, _ := gtime.StrToTime(gconv.String(fieldValue))
		return t.Format("Y-m-d"), nil

	case typeDatetime:
		if t, ok := fieldValue.(time.Time); ok {
			return gtime.NewFromTime(t), nil
		}
		t, _ := gtime.StrToTime(gconv.String(fieldValue))
		return t, nil

	default:
		return gconv.String(fieldValue), nil
	}
}

// mappingAndFilterData automatically mappings the map key to table field and removes
// all key-value pairs that are not the field of given table.
func (c *Core) mappingAndFilterData(ctx context.Context, schema, table string, data map[string]interface{}, filter bool) (map[string]interface{}, error) {
	fieldsMap, err := c.db.TableFields(ctx, c.guessPrimaryTableName(table), schema)
	if err != nil {
		return nil, err
	}
	fieldsKeyMap := make(map[string]interface{}, len(fieldsMap))
	for k := range fieldsMap {
		fieldsKeyMap[k] = nil
	}
	// Automatic data key to table field name mapping.
	var foundKey string
	for dataKey, dataValue := range data {
		if _, ok := fieldsKeyMap[dataKey]; !ok {
			foundKey, _ = gutil.MapPossibleItemByKey(fieldsKeyMap, dataKey)
			if foundKey != "" {
				if _, ok = data[foundKey]; !ok {
					data[foundKey] = dataValue
				}
				delete(data, dataKey)
			}
		}
	}
	// Data filtering.
	// It deletes all key-value pairs that has incorrect field name.
	if filter {
		for dataKey := range data {
			if _, ok := fieldsMap[dataKey]; !ok {
				delete(data, dataKey)
			}
		}
	}
	return data, nil
}
