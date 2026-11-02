# unlocked-product-template

The starting point for every Unlocked product repo (EN-202). Create a new
product from it with GitHub's **Use this template** button, then follow
[New product checklist](#new-product-checklist).

It gives a product:

- **One Go binary, one image:** `app serve` runs the API, `app worker` runs the
  background worker.
- **`GET <PATH_PREFIX>/api/version`**, the same JSON as unlocked-core's and the
  tutor's: `{"version": "...", "commit": "...", "branch": "..."}`.
- **`GET <PATH_PREFIX>/api/healthcheck`**, which returns `OK`, the same as
  core's. Point the Deployment's readiness probe at it.
- **Routing under a path prefix:** every route lives under `PATH_PREFIX`, the
  path ingress sends this product's requests to.
- **CI:** tests, golangci-lint and govulncheck on every push, conventional-commit
  checks, and an image pushed to ECR from `main` and from each release.
- **release-please:** versions and the changelog come from the commit messages.

There's no Kubernetes chart here. Every product's templates live in the
`unlocked-platform` chart (EN-183).

## Run it locally

Needs Go 1.26 and Docker.

```sh
make run      # API on :8080, under /<repo name>
make worker   # the worker
make test     # go test -race
make lint     # golangci-lint, in Docker
make build    # the image, named after the repo
```

```sh
curl localhost:8080/<repo name>/api/version
```

Settings, all from the environment:

| Variable | Default | What it does |
|---|---|---|
| `ADDR` | `:8080` | Where the API listens |
| `PATH_PREFIX` | none (root) | The path every route lives under, such as `/example` (`example` and `/example/` work too). Between slashes it may have letters, digits, `-`, `.`, `_` and `~`; anything else stops `serve` with an error. |
| `APP_VERSION`, `GIT_COMMIT`, `GIT_BRANCH` | `unknown` | What `/api/version` reports. CI bakes them into the image. |

## How to release

1. Merge PRs to `main` with conventional-commit titles (`feat: ...`,
   `fix: ...`). Each merge pushes `:sha-<short>` to ECR, then moves `:latest`
   to it if it's still the tip of `main`.
2. release-please keeps a `chore(main): release X.Y.Z` PR open with the
   changelog. A `feat` bumps the minor version, a `fix` the patch (before
   1.0.0, a breaking change bumps the minor version).
3. Merge that PR when you want a release. It tags `vX.Y.Z`, and CI pushes
   `:X.Y.Z` to ECR.

### Commit types

The `conventional` and `pr title` checks allow these types, and nothing else:

| Type | In the changelog? | Version bump |
|---|---|---|
| `feat` | yes | minor |
| `fix`, `perf`, `revert` | yes | patch |
| `chore`, `docs`, `style`, `refactor`, `test`, `build`, `ci` | no | none on its own |

A `!` after any type (`feat!:`, `refactor!:`) marks a breaking change: it
always shows in the changelog and bumps the major version (the minor before
1.0.0). A scope is optional: `fix(worker): ...`.

## How to deploy

In `unlocked-platform`:

1. **First time only:** add the product's templates to the chart behind
   `components.<product>`: a Deployment for `serve` with a readiness probe on
   `<PATH_PREFIX>/api/healthcheck` (and one for `worker` if it has background
   jobs), a Service, ingress paths for its `PATH_PREFIX`, and a
   per-revision migrate Job if it has tables.
2. Pin the release in each site file (`images.<product>.tag: X.Y.Z`) and
   deploy through the deploy workflow. Only staging may use `latest`.

## Vulnerability alerts in Slack

`govulncheck` checks the code, its dependencies and the Go version against the
Go vulnerability database. It's a required check on every push, it runs on
`main` every Monday at 14:00 UTC so new vulnerabilities are caught without a
push, and it can be run by hand (**Actions → govulncheck → Run workflow**).

When it fails on `main`, it posts one line to Slack: `UnlockedLabs/<repo>:
govulncheck failed on main.` and a link to the run. Failures on other branches
don't post; the PR's red check shows them. It posts only if the repo can see a
secret named `SLACK_WEBHOOK_URL`. It's a secret, not a variable, because anyone
with the URL can post to the channel. Without it, the run logs a notice and
posts nothing.

**Still to decide (EN-205):** one organization secret shared by every product
repo, or a secret per repo, and which channel the alerts go to. Until then,
repos run without it.

### Get a webhook URL

The URL comes from Slack. GitHub never shows a secret's value again, so it
can't be copied from core's `SLACK_WEBHOOK_URL`.

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and sign in to the
   Unlocked workspace.
2. Open the app that already posts our alerts (core's go to
   `#unlockedv2-alerts`). If there isn't one, **Create New App → From
   scratch**, name it (such as "Unlocked CI alerts"), pick the workspace and
   **Create App**.
3. **Features → Incoming Webhooks**, and turn on **Activate Incoming
   Webhooks**.
4. **Add New Webhook to Workspace**, pick the channel and **Allow**. A
   workspace admin may have to approve a new app first.
5. Copy the URL (`https://hooks.slack.com/services/...`). Each URL posts to
   one channel; a different channel needs a new webhook.

### Give it to the repo

**As an organization secret** (set up once; an org admin does it):

1. Org **Settings → Secrets and variables → Actions → New organization
   secret**.
2. Name `SLACK_WEBHOOK_URL`, the URL as the value, **Repository access:
   Selected repositories**, pick the repos, **Add secret**.

For each new repo after that, open the secret, add the repo under **Repository
access** and **Save**. Or, with a token that has the `admin:org` scope
(`gh auth refresh -h github.com -s admin:org`):

```sh
gh api -X PUT orgs/UnlockedLabs/actions/secrets/SLACK_WEBHOOK_URL/repositories/$(gh api repos/UnlockedLabs/<repo> --jq .id)
```

Don't use `gh secret set --org ... --repos ...` to add a repo: it replaces the
secret's whole repo list.

**As a repo secret** (a repo admin does it); `gh` asks for the URL, so it stays
out of your shell history:

```sh
gh secret set SLACK_WEBHOOK_URL -R UnlockedLabs/<repo>
```

Or repo **Settings → Secrets and variables → Actions → New repository secret**.

### Check it

The repo can see the secret if its name is listed by one of these:

```sh
gh secret list -R UnlockedLabs/<repo>
gh api repos/UnlockedLabs/<repo>/actions/organization-secrets --jq '.secrets[].name'
```

The first lists repo secrets, the second organization secrets shared with the
repo. To check the URL itself, post a test message to its channel:

```sh
curl --fail -X POST -H 'Content-Type: application/json' --data '{"text":"test from <repo>"}' '<url>'
```

### Change or revoke the URL

If the URL leaks or the channel changes, remove the webhook in the Slack app's
**Incoming Webhooks** page (that kills the old URL), add a new one, and update
the secret. Updating an organization secret updates every repo that uses it.

### When the weekly run stops

GitHub turns off scheduled workflows in a public repo after 60 days with no
activity. Turn it back on at **Actions → govulncheck → Enable workflow**.

### When the check fails

- **A finding in a dependency:** `go get <module>@<fixed version>`, then
  `go mod tidy`, in a PR.
- **A finding in the standard library:** a newer Go patch release fixes it.
  The check and the image both use the newest patch of the Go version in
  `go.mod`, so rerun the check once that release is out, and merge something
  so `main` builds a new image with it.

## New product checklist

Replace `<repo>` with the new repo's name, such as `unlocked-open-content`.

1. **Create the repo** from this template under UnlockedLabs. Its first runs
   fail until steps 4 to 6 are done: `build-and-push` can't get AWS
   credentials, `release-please` has no bot, and GitHub's own "Initial commit"
   isn't a conventional commit. That's expected.
2. **Rename the module** (Go files only; this README keeps the template's name
   until step 9 replaces it). `perl -pi` works the same on Linux and macOS,
   unlike `sed -i`:
   ```sh
   grep -rl unlocked-product-template --include='*.go' --include=go.mod . \
     | xargs perl -pi -e 's#unlocked-product-template#<repo>#g'
   ```
3. **Branch protection on `main`:** copy the template's `protect-main`
   ruleset (PRs with one approval, and the `vet and test`, `golangci-lint`,
   `govulncheck`, `conventional` and `pr title` checks). One line, so it works
   in bash and fish:
   ```sh
   gh api repos/UnlockedLabs/unlocked-product-template/rulesets/$(gh api repos/UnlockedLabs/unlocked-product-template/rulesets --jq '.[] | select(.name=="protect-main") | .id') --jq '{name, target, enforcement, conditions, rules, bypass_actors}' | gh api -X POST repos/UnlockedLabs/<repo>/rulesets --input -
   ```
   `.github/CODEOWNERS` asks `@UnlockedLabs/reviewers` to review every PR, so
   check the team can write to the repo (`maintain` or `push`):
   ```sh
   gh api repos/UnlockedLabs/<repo>/teams --jq '.[] | "\(.slug) \(.permission)"'
   ```
4. **Release bot:** install the unlocked-release-bot GitHub App on the repo
   (the App's **Install App** page, **Configure**, then add the repo). Add the
   variable `RELEASE_APP_CLIENT_ID` (`Iv23liURFfuC0NzSCm4G`) and the secret
   `RELEASE_APP_PRIVATE_KEY`. For a key, open the App's settings, then
   **Credentials → Key pairs → New key**; paste the whole `.pem` and delete
   the file.
5. **ECR:** create an ECR repository named `<repo>` in `us-west-2`, with
   mutable tags (the default), since every merge to `main` moves `:latest`:
   ```sh
   aws ecr create-repository --repository-name <repo> --region us-west-2 --image-tag-mutability MUTABLE
   ```
   In the console, leave **Tag immutability** on **Mutable**.
6. **AWS access for CI:** add the repo to the trust policy of the
   `GithubActionsBuildPushEcr` role. New repos get the immutable OIDC subject
   `repo:UnlockedLabs@<owner id>/<repo>@<repo id>:*` (EN-187); check the
   prefix with `gh api repos/UnlockedLabs/<repo>/actions/oidc/customization/sub`
   and the ids with `gh api repos/UnlockedLabs/<repo> --jq '.owner.id, .id'`.
   Then add the role's ARN as the secret `AWS_IAM_ROLE`.
7. **Vulnerability alerts in Slack:** give the repo the `SLACK_WEBHOOK_URL`
   secret, as in [Vulnerability alerts in Slack](#vulnerability-alerts-in-slack).
   Without it, `govulncheck` still runs and still blocks PRs; it just doesn't
   post to Slack.
8. **Private Go module?** If other repos import it, set
   `GOPRIVATE=github.com/UnlockedLabs/*` where it's built, and give CI a read
   token.
9. **README:** replace this one with what the product is, how to run it
   locally, and how to release it.
10. **Platform:** add its templates and pin its tag, as in
    [How to deploy](#how-to-deploy).
