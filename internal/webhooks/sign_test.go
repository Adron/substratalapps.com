package webhooks

import (
	"strings"
	"testing"
	"time"
)

func TestSignTwoSecrets(t *testing.T) {
	h := Sign([]byte(`{"id":"wev_1"}`), time.Unix(1730649761, 0), "whsec_new", "whsec_old")
	parts := strings.Split(h, ",")
	if parts[0] != "t=1730649761" || len(parts) != 3 || !strings.HasPrefix(parts[1], "v1=") {
		t.Fatalf("header = %s", h)
	}
	if parts[1] == parts[2] {
		t.Fatal("each secret signs separately")
	}
}

func TestRetrySchedule(t *testing.T) {
	if len(RetrySchedule) != maxAttempts-1 {
		t.Fatalf("6 attempts means 5 retries, got %d", len(RetrySchedule))
	}
}
