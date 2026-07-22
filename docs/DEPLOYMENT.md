# Deployment

The server (`cmd/server`) is a stateless Go HTTP service configured entirely
through environment variables (12-factor — no config files) and shipped as a
small container image. It can run anywhere: Kubernetes, a container host, or a
plain VM.

## Build the image

```sh
docker build -t judge-server .
```

Multi-stage build → a ~23 MB `distroless/static` image that includes CA
certificates (needed for outbound HTTPS to the OpenAI and Anthropic APIs).

## Runtime requirements

- **Environment variables** (there is no `.env` in the image — inject them via
  your platform's secrets/env):
  `DATABASE_URL`, `EMBEDDING_API_KEY`, `EMBEDDING_MODEL`, `LLM_API_KEY`,
  `LLM_MODEL`, `PORT` (defaults to `8090`).
- **A Postgres database** with the `vector` and `pg_trgm` extensions (see
  *Vector extension* below).
- **Outbound HTTPS** to `api.openai.com` and `api.anthropic.com` — check the
  egress policy if the environment restricts outbound traffic.
- **Health endpoint** `GET /healthz` (pings the DB) for liveness/readiness probes.

## Vector extension: pgvector or a compatible engine

The app deliberately uses only pgvector's standard `vector` type and distance
operator (`<=>`), and **no ANN index** (a brute-force scan is plenty at this
corpus size — see `Claude.md`). That means it runs unchanged on any
**pgvector-compatible** engine, including
[VectorChord](https://docs.vectorchord.ai/), which is a superset of pgvector.

- Plain pgvector: `CREATE EXTENSION IF NOT EXISTS vector;` (see `init.sql`).
- VectorChord: `CREATE EXTENSION IF NOT EXISTS vchord CASCADE;` installs pgvector
  (and thus the `vector` type) as a dependency; the app's queries are identical.

Whichever you use, the database also needs `pg_trgm` for fuzzy card lookup. The
`init.sql` in this repo creates both extensions and the schema; on a managed or
shared cluster you may instead apply the schema once with the right privileges.

## Database seeding (one-off)

The ingestion CLI populates the database. Run it once against the target DB
(e.g. as a one-shot Kubernetes `Job`, or locally via `kubectl port-forward` with
`DATABASE_URL` pointed at the remote):

```sh
go run ./cmd/ingest        # or run the same binary from the built image
```

Re-run it later to refresh rules/cards (it upserts). Only the embedding step
needs `EMBEDDING_API_KEY`.

## Protecting the API (recommended for any public deployment)

The API calls paid LLM/embedding endpoints, so an openly reachable server can
run up a bill. The server has built-in **OIDC** bearer-token authentication that
works with any standard provider (Authentik, Keycloak, Auth0, ...). Enable it by
setting two environment variables:

- `OIDC_ISSUER` — the provider's issuer URL. The server discovers the provider's
  signing keys from `<issuer>/.well-known/openid-configuration` at startup, so
  the issuer must be reachable when the server starts.
- `OIDC_AUDIENCE` — the audience (client ID) that tokens must be issued for.

When both are set, every request except `GET /healthz` must carry a valid
`Authorization: Bearer <JWT>`; anything missing/expired/wrong-audience or signed
by a key the provider doesn't publish gets `401`. Validation is self-contained
and offline: the server caches the provider's public keys (JWKS) and verifies
each token's signature and claims locally — it never calls the provider per
request.

When `OIDC_ISSUER` is unset the API is left open and the server logs a warning
at startup. That is fine for local development but should not be used for a
hosted deployment.

> The app-side login flow (Authorization Code + PKCE) that obtains these tokens
> is on the roadmap (see `docs/ROADMAP.md` §6); it requires a custom mobile build
> rather than Expo Go.

### Rate limiting

As a second layer (and the only cost guard when auth is off), the server applies
a per-client token-bucket rate limit:

- `RATE_LIMIT_RPM` — sustained requests per minute per client (default `60`; set
  to `0` to disable).
- `RATE_LIMIT_BURST` — how many requests may arrive back-to-back (default `15`).

A limited request gets `429 Too Many Requests` with a `Retry-After` header. The
key is the authenticated subject (the JWT `sub`) when auth is enabled — spoof-proof
and robust to many clients sharing one IP behind NAT — and the client IP
otherwise. Because it keys on the validated subject rather than a forwarding
header, it works correctly behind a reverse proxy without trusting
`X-Forwarded-For`.

Always also set a hard monthly spend cap in the OpenAI and Anthropic dashboards
as a backstop.
