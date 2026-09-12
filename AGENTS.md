# billy

A self-hosted Alexa skill endpoint for [Audiobookshelf](https://www.audiobookshelf.org/), shipped as a single static executable with Tailscale Funnel ingress embedded via `tsnet`.

**Current scope**: reach the box from the public internet over Funnel, verify and capture Alexa requests, and play one externally configured, non-sensitive MP3 with basic AudioPlayer transport handling. Signature verification is enforced on `/alexa`; there is no Audiobookshelf integration, listener identity, or durable playback state yet. See `setup.md` and the static-play plan in the wiki.

## Go version

This project uses **Go 1.27**, which was released after the AI knowledge cutoff. Do not rely on training data for Go stdlib or dependency APIs — always fetch current documentation via Context7 before using an unfamiliar API.

All JSON encoding and decoding uses Go 1.27's `encoding/json/v2` (and
`encoding/json/jsontext` where needed). Importing the v1 `encoding/json` package
is categorically forbidden; the repository-wide `depguard` rule enforces this
for production code, tests, and tools.

## Build and test

```
just verify    # fmt + build + lint + test (run before committing)
just build     # produces dist/billy
just start     # run billy in the background, capturing to dist/capture
just stop      # stop it and wait for the tsnet state lock to be released
just test      # go test ./...
just snapshots # regenerate go-snaps golden files for review
just fmt       # gofmt -w .
just lint      # golangci-lint run ./...
just xbuild    # goreleaser snapshot build across the full release matrix
```

Alexa skill recipes (`skill-render`, `skill-deploy`, `skill-talk`,
`skill-corpus`) need `ask-cli` and `jq` on PATH; they are not part of `verify`
and never run in CI.

`just start` needs `BILLY_SKILL_ENDPOINT` and `BILLY_BOOK_DESCRIPTOR`:
readiness is checked over the public Funnel URL, as the tsnet listener has no
loopback address to poll, and startup validates the external book before
opening that listener.

Tool versions (Go, golangci-lint, goreleaser, just, wait4x) are pinned in `mise.toml`; run `mise install` to match CI. CI reads the same versions via `mise current`.

## Unit tests

- Use `github.com/stretchr/testify/assert` for independent checks and
  `github.com/stretchr/testify/require` for prerequisites after which the test
  cannot continue. **All assertions use Testify**: do not call `t.Error`,
  `t.Errorf`, `t.Fatal`, `t.Fatalf`, `t.Fail`, or `t.FailNow`, and do not build
  generic hand-written assertion helpers that duplicate Testify.
- Prefer the semantic Testify assertion (`NoError`, `ErrorIs`, `Equal`,
  `Contains`, `Len`, and so on) over asserting a precomputed boolean. Remember
  that `Equal` is type-sensitive; use `EqualValues` only when the production
  comparison intentionally permits equivalent numeric types.
- Use `require` only from the test goroutine. Collect worker-goroutine errors
  and assert them from the test goroutine.
- Every golden test uses `github.com/gkampitakis/go-snaps/snaps`; do not add
  ad-hoc `testdata/*.golden` readers or update flags. Use `MatchJSON` for JSON
  and `MatchSnapshot` for other values. Commit generated `__snapshots__` files,
  regenerate them with `just snapshots`, and review their diffs rather than
  editing them by hand.
- A package containing go-snaps tests must call `snaps.Clean` from its
  `TestMain` so obsolete snapshots fail the normal test run and can be removed
  during regeneration.

## Cross-platform rules

Linux, macOS and Windows on `amd64` and `arm64` are equal targets, all tested in CI: use `path/filepath` for paths, keep filenames legal on Windows, build-tag anything POSIX-only, and keep `CGO_ENABLED=0`.

## Project layout

`pkg/alexaverify/` is a **public** package: Alexa request verification, whose exported surface is a semver contract. Everything else is application logic in the repository root, and `main` consumes `alexaverify` as an ordinary external consumer.

The Alexa skill is managed from files, not the developer console: `just
skill-deploy` renders `alexa/skill-package` and uploads it, `just skill-corpus`
replays scripted utterances to fill the capture directory. See
`docs/alexa-skill-setup.md`. The endpoint URL is never committed — the manifest
holds a placeholder that is substituted from `BILLY_SKILL_ENDPOINT` at render
time.

## Commits and PR titles

Use Conventional Commits for all commit messages and PR titles. `pr-title.yml` enforces the format on PRs (the squash-merge commit is taken from the PR title). Unlike boxed, **commit types here carry version-bump semantics**: release-please is wired up, so `feat:` drives a minor bump and `fix:` a patch bump, and a `!`/`BREAKING CHANGE` drives a major.

| Type | When to use |
|---|---|
| `feat: <description>` | new user-visible feature (minor bump) |
| `fix: <description>` | bug fix (patch bump) |
| `chore:`, `docs:`, `refactor:`, `test:`, `ci:` | maintenance, no release |

## Key conventions

- Captured bodies hold live `apiAccessToken`, `userId` and `deviceId`. Never commit or paste one.
- The request body is read once and never re-encoded: the signature covers those exact bytes.
- Only verified requests are captured.
- A capture failure must not fail the request. Log it, still return a valid envelope.
- The Tailscale auth key comes from the environment only. Never a flag, a file, or a log line.
- Service configuration is environment variables, not flags. The external book descriptor is runtime content named by `BILLY_BOOK_DESCRIPTOR`.
- `http.Server` always gets an explicit `ReadHeaderTimeout` and `IdleTimeout`.

Signature verification is mandatory and has no disable switch. `/alexa` accepts only fresh requests whose `Signature-256` validates through an approved S3 certificate URL to the system roots; only those requests reach capture and skill handling.

## Major dependencies

Use Context7 for up-to-date documentation — do not guess at APIs. Query the Context7 ID before using any unfamiliar or version-sensitive API (Go 1.27 and several of these post-date the knowledge cutoff). Every ID below has been resolved and confirmed to exist; do not invent one.

| Library / source | Context7 ID | Notes |
|---|---|---|
| `tailscale.com/tsnet` | `/tailscale/tailscale` | Embedded tailnet node; `ListenFunnel` on 443. Use this one for the **Go API** |
| Tailscale product docs | `/websites/tailscale` | Use this one for **Funnel, ACLs, auth keys and admin console** behaviour — far broader coverage than the repo ID above |
| `github.com/urfave/cli/v3` | `/urfave/cli` | **The CLI framework. All command line argument handling uses urfave/cli v3** — never `flag`, never a hand-rolled parser. Note v3 is `cli.Command`, not v2's `cli.App`, so most v2 answers do not transfer |
| Alexa Skills Kit SDK for Node.js | `/alexa/alexa-skills-kit-sdk-for-nodejs` | One of the two normative sources for request verification; also request/response envelope shapes |
| GoReleaser | `/goreleaser/goreleaser` | Release/snapshot builds; ldflags inject `main.version`/`commit`/`date` |
| `github.com/stretchr/testify` | `/stretchr/testify` | All unit-test assertions; use `assert` for independent checks and `require` for prerequisites |
| `github.com/gkampitakis/go-snaps` | `/gkampitakis/go-snaps` | All golden/snapshot tests; JSON uses `snaps.MatchJSON` |

Not on Context7 — go to the source instead:

- **The Go stdlib** (`net/http`, `crypto/x509`, `crypto/rsa`, `path/filepath`, `log/slog`, `runtime/debug`): use upstream docs directly. Watch for Go 1.27 idioms the linter enforces, such as `errors.AsType[T](err)` over `errors.As(err, &target)`.
- **The Alexa Skills Kit SDK for Java.** It is the *other* normative source for signature verification and has no Context7 entry. Fetch the pinned files transiently from `raw.githubusercontent.com`; do **not** vendor them.

### Pinned Alexa verification sources

`pkg/alexaverify` ports the request verification in two of Amazon's own SDKs. Both are Apache-2.0, attributed in `NOTICE`, and pinned. Fetch them when working on that package rather than recalling them.

| Source | Repo | Commit | Path |
|---|---|---|---|
| Java | `alexa/alexa-skills-kit-sdk-for-java` | `e7f16b0523b24a34a7971d4df7ecbb48b4539639` | `ask-sdk-servlet-support/src/com/amazon/ask/servlet/` |
| Node | `alexa/alexa-skills-kit-sdk-for-nodejs` | `ea88cef4a72a44abf1aff6083472ba08a665862a` | `ask-sdk-express-adapter/lib/verifier/` |

Where the two disagree, take the stricter behaviour: Amazon cannot emit anything either SDK rejects. The signing plan holds the full adjudication rule.

### CLI conventions

`run(args, stdout, stderr) int` stays a pure function of its arguments so it is
testable without a process boundary. urfave/cli's default `ExitErrHandler`
writes to a package global and calls `os.Exit`, so it is replaced with a no-op
and exit-code mapping is done in `run`. Exit codes: `0` success, `1` runtime
failure, `2` usage error.
