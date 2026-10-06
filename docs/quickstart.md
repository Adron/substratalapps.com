---
layout: default
title: Quickstart
nav_order: 3
---

# Quickstart
{: .no_toc }

A runnable walkthrough: create a user, grant them an app, check their access, then turn it off. Every call here is a real endpoint documented in the [API Reference](../api-reference/) — this page just stitches them into one story. [Getting Started](../getting-started/) is where to go for orientation; this page is where to go to see it work.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

All requests below assume the [Conventions](../api-reference/conventions/) base URL and an admin bearer token:

```
export SUBSTRATAL_API=https://api.substratalapps.com/v1
export TOKEN=<an access token for a superadmin or support user>
```

## 1. Create a user

```bash
curl -s -X POST "$SUBSTRATAL_API/users" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "email": "jordan@example.com", "status": "invited" }'
```

```json
{
  "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email": "jordan@example.com",
  "status": "invited",
  "...": "..."
}
```

See [Users](../api-reference/users/). A default [Profile](../api-reference/profiles/), [Settings](../api-reference/settings/), and the `member` platform role were created alongside this — nothing further to call for those.

## 2. Confirm they own nothing yet

```bash
curl -s "$SUBSTRATAL_API/users/usr_01JAG3Z9X8QS3F6K2M4N5P6R7S/entitlements" \
  -H "Authorization: Bearer $TOKEN"
```

```json
{ "data": [], "page": { "next_cursor": null, "has_more": false } }
```

## 3. Grant access to an app

In production, the Application developer's own backend does this with its app-scoped API Key after its own billing confirms a payment (see [Workflows → Purchase → access](../workflows/#purchase--access)). Here, we grant directly, the way an admin comp or early-access grant would:

```bash
curl -s -X POST "$SUBSTRATAL_API/users/usr_01JAG3Z9X8QS3F6K2M4N5P6R7S/entitlements" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{ "application_id": "app_timetrack", "source": "admin_grant" }'
```

```json
{
  "id": "ent_01JAGA1B2C3D4E5F6G7H8J9K01",
  "application_id": "app_timetrack",
  "status": "active",
  "source": "admin_grant",
  "...": "..."
}
```

See [Entitlements](../api-reference/entitlements/). `status: active` is the whole point — this is the on/off switch, and it's now on.

## 4. Check what they can actually do

```bash
curl -s "$SUBSTRATAL_API/users/usr_01JAG3Z9X8QS3F6K2M4N5P6R7S/apps/app_timetrack/effective-permissions" \
  -H "Authorization: Bearer $TOKEN"
```

```json
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "allowed": false,
  "user_status": "invited",
  "entitlement_status": "active",
  "access_paths": [ { "source": "admin_grant", "entitlement_id": "ent_01JAGA1B2C3D4E5F6G7H8J9K01", "status": "active" } ],
  "roles": [],
  "effective_permissions": [],
  "computed_at": "2026-10-05T12:00:00Z"
}
```

`allowed` is still `false`: the user is `invited`, and access requires an *active* user. Activate them so the rest of the walkthrough works (outside a quickstart, they'd accept the emailed invitation instead):

```bash
curl -s -X PATCH "$SUBSTRATAL_API/users/usr_01JAG3Z9X8QS3F6K2M4N5P6R7S" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "status": "active" }'
```

Check again: `allowed: true`, `entitlement_status: "active"`.

`effective_permissions` is still empty, because no [Role](../api-reference/roles-and-permissions/) has been assigned yet (and `app_timetrack` here has no `default_app_role`) — owning an app and having a role inside it are different axes. See [Access Control](../access-control/).

## 5. Assign a role inside the app

```bash
curl -s -X POST "$SUBSTRATAL_API/users/usr_01JAG3Z9X8QS3F6K2M4N5P6R7S/roles/role_timetrack_admin" \
  -H "Authorization: Bearer $TOKEN"
```

Repeat step 4 and `effective_permissions` now includes whatever `role_timetrack_admin` grants (e.g. `app.timetrack.export`).

## 6. Turn it off

```bash
curl -s -X PATCH "$SUBSTRATAL_API/entitlements/ent_01JAGA1B2C3D4E5F6G7H8J9K01" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "status": "disabled", "disabled_reason": "quickstart_demo" }'
```

Repeat step 4 again: `allowed` is `false`, `entitlement_status` is now `disabled`, and `effective_permissions` is back to `[]` — the Role assignment from step 5 is still there underneath, untouched, ready the moment the entitlement is re-enabled. See [Workflows → Admin turns an app off for a user](../workflows/#admin-turns-an-app-off-for-a-user).

## What this skipped

Login/token issuance ([Auth](../api-reference/auth/)), the app-launch JWT and introspection that a *downstream app* calls rather than an admin ([Trust Model](../trust-model/)), and webhook delivery ([Webhooks](../api-reference/webhooks/)) — all real, all documented, just not needed to see the core access model work end to end.
