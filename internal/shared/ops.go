package shared

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"go.mercari.io/datastore"
)

var typeOfPropertyLoadSaver = reflect.TypeOf((*datastore.PropertyLoadSaver)(nil)).Elem()
var typeOfPropertyList = reflect.TypeOf(datastore.PropertyList(nil))

type getOps func(keys []datastore.Key, dst []datastore.PropertyList) error
type putOps func(keys []datastore.Key, src []datastore.PropertyList) ([]datastore.Key, []datastore.PendingKey, error)
type deleteOps func(keys []datastore.Key) error
type nextOps func(dst *datastore.PropertyList) (datastore.Key, error)
type getAllOps func(dst *[]datastore.PropertyList) ([]datastore.Key, error)

func GetMultiOps(ctx context.Context, keys []datastore.Key, dst interface{}, ops getOps) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Slice {
		return errors.New("datastore: dst has invalid type")
	}
	if len(keys) != v.Len() {
		return errors.New("datastore: keys and dst slices have different length")
	}
	if len(keys) == 0 {
		return nil
	}

	wPss := make([]datastore.PropertyList, len(keys))
	err := ops(keys, wPss)
	foundError := false

	merr, catchMerr := err.(datastore.MultiError)
	if catchMerr {
		// ok
		if len(merr) != len(keys) {
			panic(fmt.Sprintf("unexpected merr length: %d, expected: %d", len(merr), len(keys)))
		}
	} else if err == nil {
		merr = make([]error, len(keys))
	} else if err != nil {
		return err
	}

	elemType := v.Type().Elem()
	for idx := range keys {
		if catchMerr {
			err := merr[idx]
			if err != nil {
				foundError = true
				continue
			}
		}

		elem := v.Index(idx)
		if reflect.PtrTo(elemType).Implements(typeOfPropertyLoadSaver) {
			elem = elem.Addr()
		} else if elemType.Kind() == reflect.Struct {
			elem = elem.Addr()
		} else if elemType.Kind() == reflect.Ptr && elemType.Elem().Kind() == reflect.Struct {
			if elem.IsNil() {
				elem.Set(reflect.New(elem.Type().Elem()))
			}
		}

		if err = datastore.LoadEntity(ctx, elem.Interface(), &datastore.Entity{Key: keys[idx], Properties: wPss[idx]}); err != nil {
			merr[idx] = err
			foundError = true
		}
	}

	if foundError {
		return datastore.MultiError(merr)
	}

	return nil
}

func PutMultiOps(ctx context.Context, keys []datastore.Key, src interface{}, ops putOps) ([]datastore.Key, []datastore.PendingKey, error) {
	v := reflect.ValueOf(src)
	if v.Kind() != reflect.Slice {
		return nil, nil, errors.New("datastore: src has invalid type")
	}
	if len(keys) != v.Len() {
		return nil, nil, errors.New("datastore: key and src slices have different length")
	}
	if len(keys) == 0 {
		return nil, nil, nil
	}

	var wPss []datastore.PropertyList
	for idx, key := range keys {
		elem := v.Index(idx)
		if reflect.PtrTo(elem.Type()).Implements(typeOfPropertyLoadSaver) || elem.Type().Kind() == reflect.Struct {
			elem = elem.Addr()
		}
		src := elem.Interface()
		e, err := datastore.SaveEntity(ctx, key, src)
		if err != nil {
			return nil, nil, err
		}
		wPss = append(wPss, e.Properties)
	}

	wKeys, wPKeys, err := ops(keys, wPss)
	if err != nil {
		return nil, nil, err
	}

	return wKeys, wPKeys, nil
}

func DeleteMultiOps(ctx context.Context, keys []datastore.Key, ops deleteOps) error {
	err := ops(keys)
	if err != nil {
		return err
	}

	return nil
}

func NextOps(ctx context.Context, qDump *datastore.QueryDump, dst interface{}, ops nextOps) (datastore.Key, error) {

	// don't pass nil to ops.
	// the true query may not be KeysOnly.
	var ps datastore.PropertyList
	key, err := ops(&ps)
	if err != nil {
		return nil, err
	}

	if !qDump.KeysOnly {
		if err = datastore.LoadEntity(ctx, dst, &datastore.Entity{Key: key, Properties: ps}); err != nil {
			return key, err
		}
	}

	return key, nil
}

func GetAllOps(ctx context.Context, qDump *datastore.QueryDump, dst interface{}, ops getAllOps) ([]datastore.Key, error) {
	var dv reflect.Value
	var elemType reflect.Type
	var isPtrStruct bool
	if !qDump.KeysOnly {
		dv = reflect.ValueOf(dst)
		if dv.Kind() != reflect.Ptr || dv.IsNil() {
			return nil, datastore.ErrInvalidEntityType
		}
		dv = dv.Elem()
		if dv.Kind() != reflect.Slice {
			return nil, datastore.ErrInvalidEntityType
		}
		if dv.Type() == typeOfPropertyList {
			return nil, datastore.ErrInvalidEntityType
		}
		elemType = dv.Type().Elem()
		if reflect.PtrTo(elemType).Implements(typeOfPropertyLoadSaver) {
			// ok
		} else {
			switch elemType.Kind() {
			case reflect.Ptr:
				isPtrStruct = true
				elemType = elemType.Elem()
				if elemType.Kind() != reflect.Struct {
					return nil, datastore.ErrInvalidEntityType
				}
			}
		}
	}

	// TODO add reflect.Map support

	var wPss []datastore.PropertyList
	wKeys, err := ops(&wPss)
	if err != nil {
		return nil, err
	}

	if !qDump.KeysOnly {
		for idx, ps := range wPss {

			elem := reflect.New(elemType)

			if err = datastore.LoadEntity(ctx, elem.Interface(), &datastore.Entity{Key: wKeys[idx], Properties: ps}); err != nil {
				return nil, err
			}

			if !isPtrStruct {
				elem = elem.Elem()
			}

			dv.Set(reflect.Append(dv, elem))
		}
	}

	return wKeys, nil
}
