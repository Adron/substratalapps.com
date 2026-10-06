package core

import "github.com/Adron/substratalapps.com/internal/httpx"

// Destructive is the single classification of destructive operations
// (API Keys → Agent keys & restrict_destructive; Database Schema → api_keys
// "Enforcement"). It mirrors x-substratal-destructive in docs/openapi.yaml,
// and both the restrict_destructive gate here and the MCP server's
// destructiveHint read it, so the two can't drift. A test checks it
// against the spec.
//
// Always: every request is destructive. Otherwise When describes the
// condition, and the handler calls destructiveIf with the evaluated value.
type Destructive struct {
	Always bool
	When   string
}

// DestructiveOps maps operationId → classification.
var DestructiveOps = map[string]Destructive{
	"auth.logout":                     {When: "all_sessions is true"},
	"auth.resetMfaTotp":               {Always: true},
	"auth.revokeSession":              {Always: true},
	"users.update":                    {When: "status becomes suspended"},
	"users.delete":                    {Always: true},
	"users.suspend":                   {Always: true},
	"users.requestErasure":            {Always: true},
	"users.cancelErasure":             {Always: true},
	"applications.update":             {When: "review_status becomes rejected or suspended, or available_app_roles/permissions shrink"},
	"entitlements.update":             {When: "status becomes disabled or revoked"},
	"entitlements.delete":             {Always: true},
	"roles.update":                    {When: "permissions shrink"},
	"roles.delete":                    {Always: true},
	"roles.remove":                    {Always: true},
	"organizations.update":            {When: "status becomes suspended"},
	"organizations.updateMember":      {When: "demotes an org_admin"},
	"organizations.removeMember":      {Always: true},
	"tenants.requestTierChange":       {Always: true},
	"tenants.updateTierChangeRequest": {Always: true},
	"webhooks.delete":                 {Always: true},
	"apiKeys.update":                  {When: "restrict_destructive set to false"},
	"apiKeys.delete":                  {Always: true},
	"apiKeys.rotate":                  {Always: true},
}

func restricted(c *Call) error {
	return httpx.E(403, "destructive_operation_restricted",
		"This API Key has restrict_destructive enabled and cannot perform this operation.").With("key_id", c.p.ID)
}

// destructiveAlways rejects an always-destructive op for a restricted key.
func (c *Call) destructiveAlways() error {
	if c.p == nil || !c.p.RestrictDestructive {
		return nil
	}
	if d, ok := DestructiveOps[c.op.op]; ok && d.Always {
		return restricted(c)
	}
	return nil
}

// destructiveIf rejects a conditional-destructive request whose condition
// holds, for a restricted key.
func (c *Call) destructiveIf(cond bool) error {
	if cond && c.p != nil && c.p.RestrictDestructive {
		return restricted(c)
	}
	return nil
}
