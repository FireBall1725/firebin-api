# Contributing to firebin-api

This guide is for anyone opening a pull request here, including people driving the change through Claude. Read it before you write code; it is short on purpose.

## What this service is

The FireBin backend. Go 1.26, Postgres 16 through pgx, standard-library `net/http`. It owns the schema, the auth, and the background jobs. There is no ORM and no web framework, on purpose; match that when you add code.

## Layout

- `cmd/api/main.go` wires config, the pool, migrations, and the router.
- `internal/api/router.go` maps every route to a handler. Routes are grouped by access: `protected(...)` needs a writer, `admin(...)` needs an admin, and a bare `mux.HandleFunc` is public.
- `internal/api/handlers/` holds the HTTP handlers. They decode, call a repository, and respond.
- `internal/repository/` holds raw SQL. Every database access goes through a repository method; handlers never build SQL.
- `internal/db/migrations/` holds golang-migrate files as `NNNNNN_name.up.sql` and `.down.sql`. There are 22 today.
- `internal/providers/` holds the enrichment adapters (Digi-Key, Nexar).

## Conventions that matter

- Raw SQL only, inside a repository method. No query builders, no ORM.
- Return an empty slice, not `nil`, from any endpoint that serializes a list. A `nil` slice marshals to JSON `null` and crashes the web client on `.length`.
- New `.go` files start with the SPDX header the rest of the tree uses: `// SPDX-License-Identifier: AGPL-3.0-only` and the copyright line.
- Enforce access in the router, not the handler, by choosing `protected` or `admin`. The web client hides write controls for viewer accounts, but the API is the thing that has to reject them.
- A schema change is a new numbered migration with a working `.down.sql`. Migrations run on boot, so a bad one breaks startup.

## Before you open a pull request

Run what CI runs:

```sh
go build ./...
go vet ./...
go test ./...        # keep it at green; add tests for new behaviour
```

CI also runs golangci-lint against a Postgres service. Match the existing style and it stays quiet.

Keep commits small and focused, and write a message that says what changed and why. Do not add `Co-Authored-By` or "Generated with" trailers; the commit is authored by the person who sent it.
