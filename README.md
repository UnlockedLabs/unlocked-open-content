# unlocked-open-content

Open Content: libraries (Kiwix), videos (YouTube), helpful links, favorites,
reading activity and the content proxies. It was a module inside
unlocked-core until EN-205, and its routes answer exactly as they did there.

Its pages are still in core's frontend. The ingress sends their API paths
here (`/api/libraries`, `/api/videos`, `/api/helpful-links`,
`/api/open-content`, `/api/tags`, `/api/proxy`) and everything else to core.

## Commands

One binary, one image:

| Command | What it does |
|---|---|
| `app serve` | The API, on `ADDR`. Waits until the database is migrated. |
| `app worker` | Seeds a new database, then runs the jobs (Kiwix scrape, video downloads, retries, metadata sync) and their cron schedules. Run one replica, so each schedule fires once. Waits until the database is migrated. |
| `app migrate` | Runs the migrations. Run it after core's migrations. |
| `app seed-dev` | Local only (`APP_ENV=dev`): adds sample videos, favorites and activity for core's residents. Core's `make seed` runs it. |
| `app migrate-reset` | Local only (`APP_ENV=dev`): rolls back the migrations. Core's `make migrate-fresh` runs it before its own reset. |

## Code

Laid out like core's backend:

| Package | What it holds |
|---|---|
| `cmd/app` | The commands above. |
| `internal/models` | The tables as Go types, and the shapes the routes read and answer with. |
| `internal/database` | The connection, migrations, seed and every query, on `database.DB`. |
| `internal/handlers` | The routes, their access checks, the content proxies, metrics, and the answers to core's questions (`internal_api.go`). |
| `internal/tasks` | The worker: its jobs and their schedule. |
| `internal/server` | The HTTP server: health check, version, graceful stop. |

## Run it locally

Core's `make dev` builds and runs all of Open Content from this checkout,
next to core's, so most of the time that's all you need. To run it on its
own you need Go 1.26 and Docker, plus core's Postgres, NATS and Kratos.

```sh
make migrate  # migrations
make run      # API on :8080
make worker   # the worker
make test     # go test -race
make lint     # golangci-lint, in Docker
make build    # the image, named after the repo
```

Settings, all from the environment:

| Variable | What it does |
|---|---|
| `ADDR` | Where the API listens. Default `:8080`. |
| `APP_DSN`, or `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` | Core's database, as core reads them. |
| `NATS_URL`, `NATS_USER`, `NATS_PASSWORD` | Core's NATS. |
| `KRATOS_PUBLIC_URL` | Kratos's public API, to check the login session. |
| `OPEN_CONTENT_TOKEN` | The site's secret core asks its questions with. Core's server has the same one. `serve` won't start without it. |
| `APP_URL` | Core inside the cluster, such as `http://server:8080`: who a user is, and where thumbnails are uploaded. |
| `KIWIX_SERVER_URL` | Kiwix, for a new database's Kiwix provider. |
| `S3_BUCKET_NAME`, `AWS_REGION` | The videos bucket. Without a bucket, videos are served from `/videos`. |
| `FROM_EMAIL`, `TO_EMAIL` | Content requests, sent with SES. |
| `MIDDLEWARE_CRON_SCHEDULE` | When the Kiwix scrape and the metadata sync run. |
| `RETRY_VIDEO_CRON_SCHEDULE` | When failed downloads are retried. Default every hour, 06:00 to 20:00. |
| `PROXY_SCHEME_OVERRIDE` | `true` makes the library proxy reach Kiwix over `http` (St. Louis needs it). |
| `APP_ENV` | `dev` for local runs: no HSTS, videos from `/videos`, and only the scrape runs at start. |
| `LOG_LEVEL` | Default `info`. Logs are JSON, as core's are. |
| `APP_VERSION`, `GIT_COMMIT`, `GIT_BRANCH` | What `/api/version` reports. CI bakes them into the image. |

## How it works with core

- **Login:** each request's Kratos session is checked with
  `identity.SessionChecker` from unlocked-contracts, which asks core for the
  user's claims, feature flags included. The routes then run core's checks in
  core's order, with core's answers: department admin, admin, then the
  route's features (`internal/handlers/middleware.go`).
- **Core's questions:** core's dashboard, reports, facility list, Knowledge
  Center and demo seed ask Open Content through
  `unlocked-contracts/opencontent`. This service answers them on
  `/internal/v1/open-content/`, which the ingress never routes, and only
  with `OPEN_CONTENT_TOKEN`: the demo reset deletes activity at every
  facility.
- **Notifications:** a visit is published on NATS (`opencontent.notify`), and
  core sends it to the user's browser over its websocket.
- **Thumbnails** are uploaded to core's `/upload` and served from core's
  `/api/photos`.
- **Metrics:** `/api/metrics` serves core's request metrics for Prometheus,
  labeled by route. The Service's `prometheus.io/*` annotations point prod
  Prometheus at it, and the ingress never routes it.

## Database

Open Content's tables live in core's database, in the `open_content` schema.
Its queries run as the `open_content` role, which can write only that schema
and read only core's `users` and `facilities`: the connection switches with
`SET ROLE open_content`, so the service logs in with core's database user and
needs no password of its own.

- **Migrations** are in `internal/database/migrations` and run as core's
  database user. Their ledger is `public.goose_db_version_opencontent`, the
  one core used, so a site that ran them inside core doesn't run them again.
  Never edit a merged migration; add a new one.
- **Create new tables as `open_content.<name>`.** Migrations run as core's
  user, so an unqualified table lands in `public`, where the role can't use
  it.
- **The worker seeds a new database**, one that has never had a helpful link
  or a Kiwix provider, with the default links and the Kiwix and YouTube
  providers, before it starts the jobs. The links are visible at core's first
  facility, which core creates when it first starts, so the worker waits for
  it. A Postgres lock keeps two seeds from ever running at once.

## Tests

- `internal/handlers/routes_test.go` pins every route's status, JSON,
  access level and feature flags against the saved responses in
  `internal/handlers/testdata/routes`, which came unchanged from core.
  After an intended change, regenerate them and review the diff:

  ```sh
  go test ./internal/handlers -run 'TestRoute' -update
  ```

- The job tests run against a fake Kiwix, a fake core upload endpoint and a
  fake `yt-dlp` on `PATH`, so they need no network.
- The tests run on SQLite without Kiwix, NATS or AWS, so they can't run the
  full-text search, the library proxy to Kiwix, the content-request email,
  the jobs through NATS, or the migrations. Check those against core's
  `make dev`.

## How to release

1. Merge PRs to `main` with conventional-commit titles (`feat: ...`,
   `fix: ...`). Each merge pushes `:sha-<short>` to ECR, then moves `:latest`
   to it if it's still the tip of `main`.
2. release-please keeps a `chore(main): release X.Y.Z` PR open with the
   changelog. A `feat` bumps the minor version, a `fix` the patch (before
   1.0.0, a breaking change bumps the minor version).
3. Merge that PR when you want a release. It tags `vX.Y.Z`, and CI pushes
   `:X.Y.Z` to ECR.

The `conventional commits` and `pr title` checks allow `feat`, `fix`, `perf`,
`revert`, `chore`, `docs`, `style`, `refactor`, `test`, `build` and `ci`. A
`!` after the type marks a breaking change.

## How to deploy

The templates live in `unlocked-platform` (added in its EN-205 PR): the
`serve` Deployment, with its readiness probe on `/api/healthcheck`, and
Service; the `worker` Deployment with one replica; a migrate Job that runs
after core's; and the ingress paths above. Pin the release in each site file
and deploy through the deploy workflow. Only staging may use `latest`.

## Vulnerability alerts

`govulncheck` runs on every PR, as unlocked-core's scan does. Vulnerabilities
block the PR and post to #unlockedv2-alerts through the `SLACK_WEBHOOK_URL`
secret, with the same message core sends. Without the secret, they show only
as a red check.
