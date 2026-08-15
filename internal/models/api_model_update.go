package models

import (
	"fmt"
	"math"
	"reflect"

	"gorm.io/gorm"
)

type apiPatchField struct {
	name     string
	validate func(any) error
}

// apiFullObjectUpdate distinguishes Save/full-object updates from GORM's
// Model(&T{}).Where(...).Update(s) form. The latter has a zero-value model
// receiver and therefore can validate only the fields present in Dest.
func apiFullObjectUpdate(tx *gorm.DB) bool {
	return tx.Statement.Model == tx.Statement.Dest
}

// apiValidatePatch validates only fields that GORM will write. Statement.Dest
// retains map updates verbatim, including explicit zero values, unlike Changed
// which compares a zero-value Model receiver and can report such updates false.
func apiValidatePatch(tx *gorm.DB, fields ...apiPatchField) error {
	for _, field := range fields {
		value, present, err := apiPatchValue(tx, field.name)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if err := field.validate(value); err != nil {
			return err
		}
	}
	return nil
}

func apiPatchValue(tx *gorm.DB, name string) (any, bool, error) {
	if tx.Statement.Schema == nil {
		return nil, false, nil
	}
	field := tx.Statement.Schema.LookUpField(name)
	if field == nil {
		return nil, false, nil
	}
	dest := reflect.ValueOf(tx.Statement.Dest)
	for dest.Kind() == reflect.Ptr || dest.Kind() == reflect.Interface {
		if dest.IsNil() {
			return nil, false, nil
		}
		dest = dest.Elem()
	}
	switch dest.Kind() {
	case reflect.Map:
		selected, restricted := tx.Statement.SelectAndOmitColumns(false, true)
		valuesByDBName := make(map[string]any)
		iter := dest.MapRange()
		for iter.Next() {
			if iter.Key().Kind() != reflect.String {
				continue
			}
			mapped := tx.Statement.Schema.LookUpField(iter.Key().String())
			if mapped == nil || !apiPatchFieldSelected(mapped.DBName, selected, restricted) {
				continue
			}
			if _, duplicate := valuesByDBName[mapped.DBName]; duplicate {
				return nil, false, fmt.Errorf("api model update has duplicate aliases for one field")
			}
			valuesByDBName[mapped.DBName] = iter.Value().Interface()
		}
		if value, found := valuesByDBName[field.DBName]; found {
			return value, true, nil
		}
	case reflect.Struct:
		selected, restricted := tx.Statement.SelectAndOmitColumns(false, true)
		value, zero := field.ValueOf(tx.Statement.Context, dest)
		if selectedValue, selectedExplicitly := selected[field.DBName]; selectedExplicitly {
			return value, selectedValue, nil
		}
		if !restricted && !zero {
			return value, true, nil
		}
	}
	return nil, false, nil
}

func apiPatchFieldSelected(dbName string, selected map[string]bool, restricted bool) bool {
	if selectedValue, selectedExplicitly := selected[dbName]; selectedExplicitly {
		return selectedValue
	}
	return !restricted
}

func apiPatchString(value any, field string) (string, error) {
	v := reflect.ValueOf(value)
	if v.IsValid() && v.Kind() == reflect.String {
		return v.String(), nil
	}
	return "", fmt.Errorf("%s must be a string", field)
}

func apiPatchInt(value any, field string) (int64, error) {
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.Uint() <= math.MaxInt64 {
			return int64(v.Uint()), nil
		}
	}
	return 0, fmt.Errorf("%s must be an integer", field)
}
