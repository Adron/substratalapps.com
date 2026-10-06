// Package gen is generated from docs/openapi.yaml. Never edit gen.go by hand;
// run `make gen` (go generate ./internal/api/gen) after changing the spec.
package gen

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../docs/openapi.yaml
