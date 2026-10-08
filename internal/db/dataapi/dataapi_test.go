package dataapi

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"

	"github.com/CompositeCode/substratalapps.com/internal/db"
)

func TestParameters(t *testing.T) {
	ps, err := parameters(`select :s, :n::int, :b, :nil, :s, x::text`, db.Args{"s": "a", "n": 3, "b": true, "nil": nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 4 {
		t.Fatalf("params = %d (each name once, casts skipped)", len(ps))
	}
	kinds := map[string]string{}
	for _, p := range ps {
		switch p.Value.(type) {
		case *types.FieldMemberStringValue:
			kinds[*p.Name] = "string"
		case *types.FieldMemberLongValue:
			kinds[*p.Name] = "long"
		case *types.FieldMemberBooleanValue:
			kinds[*p.Name] = "bool"
		case *types.FieldMemberIsNull:
			kinds[*p.Name] = "null"
		}
	}
	want := map[string]string{"s": "string", "n": "long", "b": "bool", "nil": "null"}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s = %s, want %s", k, kinds[k], v)
		}
	}
	if _, err := parameters(`select :x`, db.Args{"x": []string{"no"}}); err == nil {
		t.Fatal("slices must go through db.TextArray")
	}
}

func TestTranslate(t *testing.T) {
	err := translate(&types.BadRequestException{Message: aws.String(
		`ERROR: duplicate key value violates unique constraint "users_email_mode_key"; SQLState: 23505`)})
	if name, ok := db.UniqueViolation(err); !ok || name != "users_email_mode_key" {
		t.Fatalf("translate = %v", err)
	}
	other := errors.New("boom")
	if translate(other) != other {
		t.Fatal("non-database errors pass through")
	}
}
