package transit

import (
	"reflect"
	"testing"
)

func TestCloneEmbeddedScenario_sharesNoReferenceWithTheCache(t *testing.T) {
	// The clone is written by hand, so a pointer, slice, or map field added to
	// any seeded type later would be shared between callers without anything
	// failing. Fill every field of every type the cache holds, then require
	// the clone to equal the original and to point into none of its memory.
	if n := reflect.TypeFor[embeddedScenario]().NumField(); n != 6 {
		t.Fatalf("embeddedScenario has %d fields; populate the new one below so this test covers it", n)
	}
	in := embeddedScenario{
		scenario:     filled[Scenario](t),
		vehicleTypes: []VehicleType{filled[VehicleType](t)},
		routes:       []Route{filled[Route](t)},
		stations:     []Station{filled[Station](t)},
		services:     []Service{filled[Service](t)},
		travelTimes:  filled[TravelTimes](t),
	}

	out := cloneEmbeddedScenario(in)

	if !reflect.DeepEqual(in, out) {
		t.Fatalf("clone differs from the cached scenario:\n in: %+v\nout: %+v", in, out)
	}
	assertNoSharedMemory(t, "embeddedScenario", reflect.ValueOf(in), reflect.ValueOf(out))
}

func filled[T any](t *testing.T) T {
	t.Helper()
	var v T
	fill(t, reflect.TypeFor[T]().Name(), reflect.ValueOf(&v).Elem())
	return v
}

func fill(t *testing.T, path string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(t, path+"*", v.Elem())
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(t, path+"[0]", v.Index(0))
	case reflect.Map:
		k := reflect.New(v.Type().Key()).Elem()
		e := reflect.New(v.Type().Elem()).Elem()
		fill(t, path+"[key]", k)
		fill(t, path+"[value]", e)
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(k, e)
	case reflect.Struct:
		for i := range v.NumField() {
			if !v.Field(i).CanSet() {
				t.Fatalf("%s.%s is unexported; extend this test to populate it", path, v.Type().Field(i).Name)
			}
			fill(t, path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	default:
		t.Fatalf("%s: %s fields are not handled; extend fill and assertNoSharedMemory", path, v.Kind())
	}
}

func assertNoSharedMemory(t *testing.T, path string, a, b reflect.Value) {
	t.Helper()
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Errorf("%s: clone shares the pointer with the cache", path)
			return
		}
		assertNoSharedMemory(t, path+"*", a.Elem(), b.Elem())
	case reflect.Slice:
		if a.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Errorf("%s: clone shares the backing array with the cache", path)
			return
		}
		for i := range a.Len() {
			assertNoSharedMemory(t, path+"[i]", a.Index(i), b.Index(i))
		}
	case reflect.Map:
		if a.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Errorf("%s: clone shares the map with the cache", path)
			return
		}
		for _, k := range a.MapKeys() {
			assertNoSharedMemory(t, path+"[key]", a.MapIndex(k), b.MapIndex(k))
		}
	case reflect.Struct:
		for i := range a.NumField() {
			assertNoSharedMemory(t, path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i))
		}
	}
}
