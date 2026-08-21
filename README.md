<p align="center">
  <img src=".github/assets/firebin-logo.png" width="88" alt="FireBin" />
</p>
<h1 align="center">firebin-api</h1>
<p align="center">
  The <a href="https://github.com/FireBall1725/firebin">FireBin</a> backend, a self-hosted electronics parts inventory.
</p>
<p align="center">
  <a href="https://github.com/FireBall1725/firebin">Umbrella</a> ·
  <a href="https://github.com/FireBall1725/firebin-web">Web</a> ·
  <a href="https://fireball1725.github.io/firebin-api/">API reference</a>
</p>

---

The FireBin backend and source of truth. It is a Go 1.26 service backed by Postgres 16, speaking REST over `/api/v1` on port 8080. The web client and every future client talk only to this API; it owns the schema, the auth, and the background jobs.

Track quantity, package, brand, location, and vendor pricing across your parts, scan the Data Matrix on a Digi-Key or Mouser bag to enrich a part in one call, and generate bin and part labels. This repo is one of three: `firebin-api` (here), [`firebin-web`](https://github.com/FireBall1725/firebin-web) (the React client), and [`firebin`](https://github.com/FireBall1725/firebin) (compose files and self-host docs).

FireBin is in alpha.

## Stack

- Go 1.26, standard-library `net/http` with a hand-rolled router. No web framework.
- Postgres 16 through pgx. Raw SQL in `internal/repository`, no ORM.
- golang-migrate for schema, River for background jobs (enrichment, bulk work).
- JWT access tokens, hashed refresh tokens, and `fbin_pat_` personal access tokens.

## Run it for development

The quickest path is the local stack in the `firebin` repo, which starts Postgres and this API together:

```sh
cd local && docker compose up -d --build
```

To run the API against a Postgres you already have, set two variables and go:

```sh
export DATABASE_URL='postgres://firebin:firebin@localhost:5432/firebin?sslmode=disable'
export JWT_SECRET="$(openssl rand -base64 48)"
go run ./cmd/api
```

The service applies its own migrations on boot, so a fresh database comes up ready. It listens on `:8080`.

The first account to register becomes the instance admin, and registration then closes. Set `REGISTRATION_ENABLED=true` to allow open signup instead.

## Environment

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Postgres connection string. Defaults to the local dev database above. |
| `JWT_SECRET` | Signs session tokens. Set a stable value; changing it logs everyone out. |
| `REGISTRATION_ENABLED` | `true` to allow open signup. Off by default; the first user still bootstraps as admin. |
| `DIGIKEY_CLIENT_ID` / `DIGIKEY_CLIENT_SECRET` | Optional Digi-Key V4 enrichment. You can set these in the UI instead. |

## Build and test

```sh
go build ./cmd/api      # compile the server
go test ./...           # 19 tests across 20 packages
go vet ./...            # static checks
```

CI runs the same three against a Postgres service, plus golangci-lint, on every push and pull request. A tagged release (`vYY.M.rev`, for example `v26.7.0`) builds the Docker image and pushes it to `ghcr.io/fireball1725/firebin-api`.

## API documentation

The reference is published at **https://fireball1725.github.io/firebin-api/**.

The API also documents itself at runtime. Start the server and open `http://localhost:8080/api/docs` for the Scalar reference, or fetch the raw OpenAPI spec at `/api/openapi.json`. Both are public, no token needed.

The spec is generated from swaggo annotations on the handlers. Regenerate it with `make docs` (install the CLI once: `go install github.com/swaggo/swag/cmd/swag@v1.16.4`). The generated `docs/` package is committed, and the `API Docs` workflow publishes it to GitHub Pages on a push that changes the spec.

## Enrichment

Scanning a distributor barcode matches an existing part locally at no API cost. To pull datasheets, parameters, images, and price breaks by MPN, the API calls Digi-Key first (the free V4 API) and Nexar as a fallback. Both are configured under Settings in the web client, or through the environment variables above.

## Contributing

See [CLAUDE.md](CLAUDE.md) for the architecture, the conventions, and the checks a pull request has to pass. To deploy FireBin rather than work on it, start from the [`firebin`](https://github.com/FireBall1725/firebin) repo.

## Support

Questions, updates, and works in progress: [FireBall Codes on Discord](https://discord.gg/QpV82CFfVD).

If this saved you some time, you can [buy me a sushi roll](https://ko-fi.com/fireball1725).

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).

---

<p align="center">
  <a href="https://fireball1725.ca"><img src=".github/assets/fireball-logo.png" width="38" alt="FireBall1725" /></a>
</p>
<p align="center">Built by <a href="https://fireball1725.ca">FireBall1725</a> in Ontario, Canada 🇨🇦</p>
