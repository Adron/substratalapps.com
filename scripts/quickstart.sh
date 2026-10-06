#!/usr/bin/env bash
# The docs/quickstart.md walkthrough as a script, against a running API
# (default: the local devserver). It's the Phase 1 acceptance test from
# PLAN.md, so CI and `make quickstart` run it on every change.
#
#   SUBSTRATAL_API=http://localhost:8080/v1 TOKEN=<superadmin token> scripts/quickstart.sh
#
# Without TOKEN it signs in with ADMIN_EMAIL / ADMIN_PASSWORD.
set -euo pipefail

API="${SUBSTRATAL_API:-http://localhost:8080/v1}"
need() { command -v "$1" >/dev/null || { echo "quickstart: $1 is required" >&2; exit 1; }; }
need curl
need jq

call() { # method path [json]
  local method=$1 path=$2 body=${3:-}
  local args=(-sS -X "$method" "$API$path" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json"
    -w '\n%{http_code}')
  [[ -n "$body" ]] && args+=(-d "$body")
  [[ "$method" == POST && "$path" == */entitlements ]] && args+=(-H "Idempotency-Key: $(uuidgen 2>/dev/null || date +%s%N)")
  local out; out=$(curl "${args[@]}")
  STATUS=${out##*$'\n'}; BODY=${out%$'\n'*}
}
expect() { # status jq-filter expected
  if [[ "$STATUS" != "$1" ]]; then echo "FAIL: HTTP $STATUS, want $1: $BODY" >&2; exit 1; fi
  if [[ $# -ge 3 ]]; then
    local got; got=$(jq -r "$2" <<<"$BODY")
    [[ "$got" == "$3" ]] || { echo "FAIL: $2 = $got, want $3: $BODY" >&2; exit 1; }
  fi
}
step() { printf '\n== %s\n' "$*"; }

if [[ -z "${TOKEN:-}" ]]; then
  TOKEN=$(curl -sS "$API/auth/login" -H "Content-Type: application/json" \
    -d "$(jq -n --arg e "${ADMIN_EMAIL:?set TOKEN or ADMIN_EMAIL/ADMIN_PASSWORD}" --arg p "${ADMIN_PASSWORD:?}" '{email:$e,password:$p}')" |
    jq -r .access_token)
fi

SLUG="timetrack-$(date +%s)"
APP="app_$SLUG"
step "0. An Application to grant (TimeTrack)"
call POST /applications "$(jq -n --arg s "$SLUG" '{slug:$s, name:"TimeTrack", launch_url:"https://timetrack.example.com/launch",
  owner_user_id:"usr_01JAG0SYSTEM00000000000000", available_app_roles:["admin","member"],
  permissions:[{key:("app."+$s+".export")}]}')"
expect 201 .id "$APP"

step "1. Create a user"
call POST /users "$(jq -n --arg e "jordan+$(date +%s)@example.com" '{email:$e, status:"invited"}')"
expect 201 .status invited
USER=$(jq -r .id <<<"$BODY")

step "2. Confirm they own nothing yet"
call GET "/users/$USER/entitlements"
expect 200 '.data | length' 0

step "3. Grant access to an app"
call POST "/users/$USER/entitlements" "$(jq -n --arg a "$APP" '{application_id:$a, source:"admin_grant"}')"
expect 201 .status active
ENT=$(jq -r .id <<<"$BODY")

step "4. Check what they can actually do"
call GET "/users/$USER/apps/$APP/effective-permissions"
expect 200 .allowed false
call PATCH "/users/$USER" '{"status":"active"}'
expect 200 .status active
call GET "/users/$USER/apps/$APP/effective-permissions"
expect 200 .allowed true

step "5. Assign a role inside the app"
call PATCH "/roles/role_${SLUG}_admin" "$(jq -n --arg s "$SLUG" '{permissions:[("app."+$s+".export")]}')"
expect 200
call POST "/users/$USER/roles/role_${SLUG}_admin" '{}'
expect 201
call GET "/users/$USER/apps/$APP/effective-permissions"
expect 200 '.effective_permissions[0]' "app.$SLUG.export"

step "6. Turn it off"
call PATCH "/entitlements/$ENT" '{"status":"disabled","disabled_reason":"quickstart_demo"}'
expect 200 .status disabled
call GET "/users/$USER/apps/$APP/effective-permissions"
expect 200 .allowed false
expect 200 '.effective_permissions | length' 0

printf '\nquickstart: all steps passed\n'
