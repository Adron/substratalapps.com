package db

import (
	"reflect"
	"testing"
)

func TestPositional(t *testing.T) {
	sql, args, err := Positional(`select :a, :b::timestamptz, :a, x::text, '{"k": 1}' from t where y = :b`, Args{"a": "1", "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	want := `select $1, $2::timestamptz, $1, x::text, '{"k": 1}' from t where y = $2`
	if sql != want {
		t.Fatalf("sql = %s", sql)
	}
	if !reflect.DeepEqual(args, []any{"1", int64(2)}) {
		t.Fatalf("args = %v", args)
	}
	if _, _, err := Positional(`select :missing`, Args{}); err == nil {
		t.Fatal("expected a missing-parameter error")
	}
}

func TestTextArray(t *testing.T) {
	cases := map[string][]string{`{}`: nil, `{"a","b c"}`: {"a", "b c"}, `{"q\"uote","back\\slash"}`: {`q"uote`, `back\slash`}}
	for want, in := range cases {
		if got := TextArray(in); got != want {
			t.Errorf("TextArray(%q) = %s, want %s", in, got, want)
		}
	}
	if NullTextArray(nil) != nil {
		t.Error("nil should encode as SQL null")
	}
}
