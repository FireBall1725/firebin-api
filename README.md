# firebin-api

Backend for **FireBin**, a self-hosted electronics component inventory. Go + Postgres, REST under `/api/v1`, JWT and `fbin_pat_` personal access tokens.

Part of the FireLabs line. Sibling to Librarium; same architecture and tooling.

## Run locally

From the workspace `local/` folder (starts Postgres + the API in Docker):

```
docker compose -p firebin-local up -d --build
```

Or run the API against a local Postgres directly:

```
export DATABASE_URL="postgres://firebin:firebin@localhost:5432/firebin?sslmode=disable"
export JWT_SECRET="$(openssl rand -hex 32)"
go run ./cmd/api
```

The API refuses to start without `JWT_SECRET`. Migrations run automatically on boot.

## Endpoints (current)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/health` | — | Liveness + version |
| POST | `/api/v1/auth/register` | — | Create user (first user becomes admin) |
| POST | `/api/v1/auth/login` | — | Username/password → token pair |
| POST | `/api/v1/auth/refresh` | — | Rotate refresh token → new pair |
| POST | `/api/v1/auth/logout` | — | Revoke a refresh token |
| GET | `/api/v1/me` | Bearer | Current user |
| POST | `/api/v1/tokens` | Bearer | Mint a personal access token |
| GET | `/api/v1/tokens` | Bearer | List own tokens |
| DELETE | `/api/v1/tokens/{id}` | Bearer | Revoke a token |

## Data model

Full Part / StockItem / SupplierPart (InvenTree-style) with a template/variant layer:
a template part (`1k resistor`) holds variant parts (by package/tolerance), each with
manufacturer parts (MPN + brand), supplier parts (vendor SKU + price breaks), and stock
items (quantity at a storage location). See `internal/db/migrations/`.

## Config

| Env | Default | Notes |
|---|---|---|
| `HOST` / `PORT` | `0.0.0.0` / `8080` | Listen address |
| `DATABASE_URL` | local dev DSN | Postgres connection |
| `JWT_SECRET` | — | **Required.** ≥32 random bytes |
| `JWT_ACCESS_TTL` | `30m` | Access token lifetime |
| `JWT_REFRESH_TTL` | `720h` | Refresh token lifetime |
| `REGISTRATION_ENABLED` | `true` | Allow non-first-user signups |
| `ATTACHMENT_STORAGE_PATH` | `./data/attachments` | BYO datasheet/image/STEP storage |
