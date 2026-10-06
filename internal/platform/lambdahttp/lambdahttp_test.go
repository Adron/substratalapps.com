package lambdahttp

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestAdapter(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen", r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+r.RemoteAddr)
		w.WriteHeader(201)
		_, _ = w.Write(b)
	})
	ev := events.APIGatewayV2HTTPRequest{RawPath: "/v1/users", RawQueryString: "limit=2", Body: `{"a":1}`,
		Headers: map[string]string{"authorization": "Bearer x"}}
	ev.RequestContext.HTTP.Method = "POST"
	ev.RequestContext.HTTP.SourceIP = "203.0.113.24"
	resp, err := Handler(h)(context.Background(), ev)
	if err != nil || resp.StatusCode != 201 || resp.Body != `{"a":1}` {
		t.Fatalf("resp = %+v %v", resp, err)
	}
	if resp.Headers["X-Seen"] != "POST /v1/users?limit=2 Bearer x 203.0.113.24:0" {
		t.Fatalf("seen = %s", resp.Headers["X-Seen"])
	}
}
