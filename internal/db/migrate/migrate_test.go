package migrate

import (
	"testing"
	"testing/fstest"
)

func TestSplit(t *testing.T) {
	script := `-- comment; with a semicolon
create table a (x text default 'a;b');
create function f() returns trigger language plpgsql as $$
begin
  perform 1; -- inside a body
  return new;
end $$;
/* block; comment */ insert into a values ('it''s; fine');
`
	got := Split(script)
	if len(got) != 3 {
		t.Fatalf("got %d statements: %q", len(got), got)
	}
}

func TestLoadRejectsBadNames(t *testing.T) {
	if _, err := Load(fstest.MapFS{"0001_ok.sql": {Data: []byte("select 1")}, "bad.sql": {Data: []byte("x")}}); err == nil {
		t.Fatal("expected an error for bad.sql")
	}
	if _, err := Load(fstest.MapFS{"0001_a.sql": {}, "0001_b.sql": {}}); err == nil {
		t.Fatal("expected an error for a duplicate version")
	}
	ms, err := Load(fstest.MapFS{"0002_b.sql": {}, "0001_a.sql": {}})
	if err != nil || ms[0].Version != "0001" {
		t.Fatalf("order = %v, %v", ms, err)
	}
}
