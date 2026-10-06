// Package lambdahttp adapts API Gateway HTTP API (payload v2) events to a
// standard http.Handler, so the exact handler the devserver runs on a
// laptop is what each Lambda runs in AWS (README → Technology stack).
package lambdahttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// Start runs h as the Lambda handler. It never returns.
func Start(h http.Handler) {
	lambda.Start(Handler(h))
}

// Handler converts h into a Lambda handler function.
func Handler(h http.Handler) func(context.Context, events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return func(ctx context.Context, ev events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		req, err := Request(ctx, ev)
		if err != nil {
			return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusBadRequest}, nil
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return Response(rec), nil
	}
}

// Request builds the http.Request for an event.
func Request(ctx context.Context, ev events.APIGatewayV2HTTPRequest) (*http.Request, error) {
	var body io.Reader = strings.NewReader(ev.Body)
	if ev.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(ev.Body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	u := url.URL{Path: ev.RawPath, RawQuery: ev.RawQueryString}
	req, err := http.NewRequestWithContext(ctx, ev.RequestContext.HTTP.Method, u.RequestURI(), body)
	if err != nil {
		return nil, err
	}
	for k, v := range ev.Headers {
		// HTTP API joins repeated headers with commas.
		for _, part := range []string{v} {
			req.Header.Add(k, part)
		}
	}
	for _, c := range ev.Cookies {
		req.Header.Add("Cookie", c)
	}
	req.Host = ev.RequestContext.DomainName
	// API Gateway's view of the client is the trustworthy one.
	req.RemoteAddr = ev.RequestContext.HTTP.SourceIP + ":0"
	if req.Header.Get("X-Request-Id") == "" && ev.RequestContext.RequestID != "" {
		req.Header.Set("X-Amzn-Request-Id", ev.RequestContext.RequestID)
	}
	return req, nil
}

// Response converts a recorded response.
func Response(rec *httptest.ResponseRecorder) events.APIGatewayV2HTTPResponse {
	headers := map[string]string{}
	var cookies []string
	for k, v := range rec.Header() {
		if strings.EqualFold(k, "Set-Cookie") {
			cookies = append(cookies, v...)
			continue
		}
		headers[k] = strings.Join(v, ",")
	}
	body := rec.Body.Bytes()
	out := events.APIGatewayV2HTTPResponse{StatusCode: rec.Code, Headers: headers, Cookies: cookies}
	if utf8.Valid(body) {
		out.Body = string(body)
	} else {
		out.Body, out.IsBase64Encoded = base64.StdEncoding.EncodeToString(body), true
	}
	return out
}
