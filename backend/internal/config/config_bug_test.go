package config

import (
	"reflect"
	"testing"
)

func TestSplitAndTrim_StripsSpaces(t *testing.T) {
	got := splitAndTrim("a, b,c", ",")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
