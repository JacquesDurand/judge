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
run up a bill. Put it behind authentication. The intended approach is a standard
**OIDC** provider (e.g. Authentik, Keycloak, Auth0): the client obtains a token
and sends `Authorization: Bearer <JWT>`, and the server validates it against the
provider's JWKS (issuer / audience configured via env). JWT-validation support
and the app-side login flow are on the roadmap (see `docs/ROADMAP.md` §6).

A simpler interim gate is a shared static bearer token the server checks against
a secret it reads from the environment — enough to keep random traffic out while
proper SSO is wired up.

Always also set a hard monthly spend cap in the OpenAI and Anthropic dashboards
as a backstop.
