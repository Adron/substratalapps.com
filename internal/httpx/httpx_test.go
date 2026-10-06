package httpx

import (
	"net/http/httptest"
	"testing"
)

func TestCursors(t *testing.T) {
	c := Cursors{Key: []byte("k")}
	cur := c.Encode("2026-10-05T00:00:00Z", "usr_1", "f1")
	pos, err := c.Decode(cur, "f1")
	if err != nil || pos.ID != "usr_1" {
		t.Fatalf("decode = %v, %v", pos, err)
	}
	if _, err := c.Decode(cur, "f2"); err == nil {
		t.Fatal("a cursor is only valid with its own filters")
	}
	if _, err := c.Decode(cur[:len(cur)-2]+"xx", "f1"); err == nil {
		t.Fatal("a tampered cursor is invalid")
	}
	if _, err := (Cursors{Key: []byte("other")}).Decode(cur, "f1"); err == nil {
		t.Fatal("a cursor from another key is invalid")
	}
}

func TestLimitAndFilters(t *testing.T) {
	for in, want := range map[int]int{0: 1, 25: 25, 500: 100} {
		v := in
		if got := Limit(&v); got != want {
			t.Errorf("Limit(%d) = %d", in, got)
		}
	}
	if Limit(nil) != 25 {
		t.Error("default limit is 25")
	}
	a := Filters(httptest.NewRequest("GET", "/v1/users?status=active&limit=5&cursor=x", nil))
	b := Filters(httptest.NewRequest("GET", "/v1/users?status=active", nil))
	c := Filters(httptest.NewRequest("GET", "/v1/users?status=invited", nil))
	if a != b || a == c {
		t.Fatal("filters ignore limit/cursor and nothing else")
	}
}

func TestIfMatch(t *testing.T) {
	r := httptest.NewRequest("PATCH", "/", nil)
	if CheckIfMatch(r, 3) != nil {
		t.Fatal("absent If-Match is last-write-wins")
	}
	r.Header.Set("If-Match", `W/"3"`)
	if CheckIfMatch(r, 3) != nil || CheckIfMatch(r, 4) == nil {
		t.Fatal("If-Match comparison")
	}
}
