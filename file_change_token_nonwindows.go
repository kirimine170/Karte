//go:build !windows

package main

import (
	"fmt"
	"os"
	"reflect"
)

func platformFileChangeToken(_ string, info os.FileInfo) string {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return ""
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return ""
	}

	for _, fieldName := range []string{"Ctimespec", "Ctim"} {
		field := value.FieldByName(fieldName)
		if field.IsValid() && field.Kind() == reflect.Struct {
			sec := signedIntegerField(field, "Sec")
			nsec := signedIntegerField(field, "Nsec")
			return fmt.Sprintf("%d:%d", sec, nsec)
		}
	}
	ctime := signedIntegerField(value, "Ctime")
	ctimeNS := signedIntegerField(value, "Ctimensec")
	if ctime != 0 || ctimeNS != 0 {
		return fmt.Sprintf("%d:%d", ctime, ctimeNS)
	}
	return ""
}

func signedIntegerField(value reflect.Value, name string) int64 {
	field := value.FieldByName(name)
	if !field.IsValid() {
		return 0
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return field.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(field.Uint())
	default:
		return 0
	}
}
