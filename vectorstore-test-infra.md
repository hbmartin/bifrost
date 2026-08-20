# Vectorstore Test Infra

`framework/vectorstore` talks to real databases. Its tests **do not skip when the services are
absent — they fail**, so a developer with nothing running sees 30 red tests and no hint that the
cause is environmental. This is how to bring the infra up, what it does and doesn't cover, and how
to get a clean signal when you can't.

Measured on 2026-08-20 against `dev` at `b157034bf` (macOS, arm64). The failure counts for runs
*without* the stack are observed; the two rows that assume `docker compose up -d` are derived from
the compose file and the tests' hardcoded endpoints, not from a run — the stack was never started
on this machine.

---

## The one thing to know first

`make test-all` is a plain target chain:

```make
test-all: test-core test-framework test-plugins test-http-transport test test-cli
```

It **stops at the first failing target**. `test-framework` dies on vectorstore, so `test-plugins`,
`test-http-transport`, `test`, and `test-cli` never run at all. A red `test-all` on a machine
without this infra is not a test result — it's a truncated run. Run the later targets individually
before concluding anything.

---

## Bring it up

There's a compose file for exactly this, and no `make` target wraps it:

```bash
cd framework
docker compose up -d
docker compose ps          # wait until every service is (healthy)
```

Every service defines a healthcheck, so `docker compose ps` is the honest readiness signal — the
tests connect immediately and will fail against a container that is up but still starting.

Tear down with `docker compose down`, or `docker compose down -v` to also drop the named volumes
(`postgres_data`, `clickhouse_data`, `weaviate_data`, `qdrant_data`).

### What it starts

| Service | Image | Host port | Used by |
|---|---|---|---|
| Redis | `redis/redis-stack:latest` | `6379` | Redis vector store |
| Qdrant | `qdrant/qdrant:v1.16.3` | `6333` REST, `6334` gRPC | Qdrant vector store |
| Weaviate | `weaviate:1.25.0` | `9000` → container `8080`, `50051` | Weaviate vector store |
| Pinecone Local | `pinecone-io/pinecone-index:latest` | `5081` | Pinecone vector store |
| Postgres | `postgres:16-alpine` | `5432` | configstore / logstore |
| ClickHouse | `clickhouse-server:24.8-alpine` | `9001` → container `9000`, `8123` HTTP | logstore ClickHouse tests |

Two port choices are deliberate and easy to misread: Weaviate takes host `9000`, which is why
ClickHouse's native protocol is remapped to host `9001`; and Qdrant's tests use the **gRPC** port
`6334`, not the REST port `6333`.

Pinecone Local is seeded as `serverless` / `dense` / `DIMENSION: 1536` / `METRIC: cosine` — 1536
matches `text-embedding-3-small`. Changing the dimension breaks the vector tests.

### On Apple Silicon

The Pinecone image is pinned `platform: linux/amd64`. On arm64 it runs under emulation: slow to
start and the most likely service to flake or time out. If Pinecone is the only thing failing,
suspect emulation before suspecting your code.

---

## What compose does *not* cover

Four Redis TLS tests dial endpoints that **no service in the compose file provides**:

| Test | Endpoint | Provided? |
|---|---|---|
| `TestNewRedisStore_ConfiguresStandaloneTLSClient` | `localhost:6380` | no |
| `TestNewRedisStore_ConfiguresStandaloneTLSClientWithCACert` | `localhost:6380` | no |
| `TestNewRedisStore_ConfiguresClusterTLSClient` | `localhost:7100` | no |
| `TestNewRedisStore_ConfiguresClusterTLSClientWithCACert` | `localhost:7100` | no |

Those addresses are hardcoded in `framework/vectorstore/redis_test.go` (lines 273, 291, 309, 327).
They read no environment variable and carry no skip guard, so there is no supported way to make
them pass locally — they need a TLS-enabled standalone Redis on `6380` and a TLS Redis **cluster**
on `7100`, neither of which the repo defines.

Despite the names, these aren't pure config-assembly tests: `NewRedisStore` connects during
construction, so the assertion about client options is never reached.

**Practical consequence:** even with `docker compose up -d`, expect these 4 to fail. A fully green
`test-framework` is not currently achievable on a developer machine.

---

## When you can't run the infra

The integration tests are guarded by `testing.Short()` — 10 guards in `redis_test.go`, 7 each in
`pinecone_test.go`, `qdrant_test.go`, and `weaviate_test.go`:

```bash
cd framework
go test -short -count=1 ./vectorstore/
```

Measured result: **25 skipped, 5 still failing** — the 4 TLS tests above, plus
`TestRedisConfig_Validation`, which dials plain `localhost:6379` with no short guard.

So the escape hatches stack up like this:

| Setup | Failures in `vectorstore` |
|---|---|
| Nothing running | 30 |
| `-short`, nothing running | 5 |
| `docker compose up -d` | 4 (the TLS tests) |
| `docker compose up -d` + `-short` | 4 (the TLS tests) |

To get a genuinely clean signal from the rest of the framework, scope around the package:

```bash
go test ./... $(go list ./... | grep -v vectorstore)
```

---

## Environment overrides

The test setups are environment-driven, so you can point them at services you already run instead
of the compose stack.

| Variable | Default | Notes |
|---|---|---|
| `REDIS_ADDR` | `localhost:6379` | |
| `REDIS_USERNAME` / `REDIS_PASSWORD` | empty | |
| `REDIS_DB` | `0` | |
| `REDIS_USE_TLS` | empty | |
| `REDIS_INSECURE_SKIP_VERIFY` | empty | |
| `REDIS_CLUSTER_MODE` | empty | |
| `REDIS_TIMEOUT` | `10s` | falls back to the default on a parse error |
| `QDRANT_HOST` / `QDRANT_PORT` | `localhost` / `6334` | gRPC port |
| `QDRANT_API_KEY` / `QDRANT_USE_TLS` | empty | |
| Weaviate host | `localhost:9000` | constant, no env override |
| Pinecone index host | `localhost:5081` | constant; cloud runs use `PINECONE_API_KEY` + `PINECONE_INDEX_HOST` |

---

## Diagnosing a failure

Every infra-caused failure looks the same — `connection refused` — and the port tells you which
service is missing:

| Port in the error | Missing service |
|---|---|
| `6379` | Redis |
| `6380`, `7100` | Redis TLS / TLS cluster — **not provided by compose** |
| `6334` | Qdrant (gRPC) |
| `5081` | Pinecone Local |
| `9000` | Weaviate |
| `9001` | ClickHouse (native) |
| `5432` | Postgres |

Anything that is *not* `connection refused` — an assertion diff, a panic, a dimension mismatch —
is a real failure and worth reading.

Before blaming a branch for vectorstore failures, confirm the package even changed:

```bash
git diff --quiet <base> <branch> -- framework/vectorstore && echo "unchanged - not this branch"
```

---

## Also worth knowing

Three unrelated traps sit next to this one and produce equally misleading results:

- **`make test` cannot pass right now, and it isn't your branch.** That target runs
  `GOWORK=off`, which resolves the *published* module versions instead of the local workspace.
  `transports/bifrost-http/server/batch_accounting.go` imports
  `github.com/maximhq/bifrost/framework/batchaccounting` — a package that exists only in the local
  tree, since upstream's batch-accounting work landed before any framework release contained it.
  No published `framework` version provides it, so the build fails before a single test runs.
  Verified 2026-08-20 by building `upstream/dev` alone in a clean worktree: it fails identically.
  Use `go build ./...` / `go test ./...` with the workspace active instead.

- **`make` aborts before running anything** unless `USE_INFISICAL=0` is set, because `EXPOSE_ENV`
  defaults to sourcing secrets from an Infisical CLI that may not be installed.
- **`gotestsum` installs to `GOPATH/bin`**, which isn't on a non-login shell's `PATH`. When it's
  missing, the target reports success having run **zero** tests. Always check the test count.

```bash
env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GOOGLE_API_KEY \
    USE_INFISICAL=0 PATH="$(go env GOPATH)/bin:$PATH" \
    make test-framework
```

Scrubbing the provider keys matters separately: with them exported, `core`'s live suites hit real
providers and hang on the 10-minute default timeout.
