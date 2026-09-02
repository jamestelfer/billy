# billy

A self-hosted Alexa skill endpoint for [Audiobookshelf](https://www.audiobookshelf.org/), shipped as a single static executable with Tailscale Funnel ingress embedded via `tsnet`.

**Current scope** is deliberately small: reach the box from the public internet over Funnel, and capture the raw bytes of every Alexa request to disk. There is no signature verification, no ABS integration and no AudioPlayer support yet — those are separate, later plans. See `setup.md` for the full plan and its phase boundaries.

## Go version

This project uses **Go 1.27**, which was released after the AI knowledge cutoff. Do not rely on training data for Go stdlib or dependency APIs — always fetch current documentation via Context7 before using an unfamiliar API.

## Build and test

```
just verify    # fmt + build + lint + test (run before committing)
just build     # produces dist/billy
just start     # run billy in the background, capturing to dist/capture
just stop      # stop it and wait for the tsnet state lock to be released
just test      # go test ./...
just fmt       # gofmt -w .
just lint      # golangci-lint run ./...
just xbuild    # goreleaser snapshot build across the full release matrix
```

Alexa skill recipes (`skill-render`, `skill-deploy`, `skill-talk`,
`skill-corpus`) need `ask-cli` and `jq` on PATH; they are not part of `verify`
and never run in CI.

`just start` needs `BILLY_SKILL_ENDPOINT`: readiness is checked over the public
Funnel URL, as the tsnet listener has no loopback address to poll.

Tool versions (Go, golangci-lint, goreleaser, just, wait4x) are pinned in `mise.toml`; run `mise install` to match CI. CI reads the same versions via `mise current`.

## Cross-platform rules

Linux, macOS and Windows on both `amd64` and `arm64` are **firm, equally weighted targets** — Windows is the most likely host in practice, not a best-effort port. Consequently:

- Build every filesystem path with `path/filepath`. Never `path`, never string concatenation with `/`.
- No POSIX-only syscall without a build-tagged equivalent for the other platforms (follow boxed's `managed_darwin.go` / `managed_other.go` pattern).
- Filenames must be legal everywhere: no colons (so no bare RFC3339 timestamps), no reserved Windows device names, no trailing dots or spaces.
- `CGO_ENABLED=0` throughout — every artifact is a single static executable.
- The CI test job runs on Linux, macOS **and** Windows. A change that only passes on one OS is not done. The `build` job runs `just xbuild` so a break in the release matrix surfaces without waiting for a tag.

## Project layout

```
main.go        entry point: flags, config, tsnet Funnel listener, graceful shutdown
version.go     build metadata + buildVersion() fallback via debug.ReadBuildInfo
config.go      configuration resolved from the environment
server.go      http.Server construction, serve loop and drain, decoupled from tsnet
handlers.go    route table; /healthz
alexa.go       /alexa: bounded read, capture, minimal Alexa response envelope
capture.go     byte-exact request capture to disk (body + JSON metadata sidecar)
alexa/         ASK CLI project: skill manifest, interaction model, dialog corpus
docs/          Tailscale and Alexa skill setup
setup.md       the implementation plan this repo is being built against
```

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

- **Captured bodies are secret material.** Alexa request envelopes carry `session.user.userId`, `context.System.device.deviceId` and `context.System.apiAccessToken` — the last is a live bearer token. Never commit a capture, never paste one into an issue or a chat log, and scrub those fields before any body becomes a test fixture. The capture directory is gitignored.
- **The body is persisted byte-for-byte.** Never unmarshal-then-remarshal, reformat, or normalise a captured body: the Alexa signature is computed over the raw bytes, so a round-tripped body is worthless as a corpus.
- **The Tailscale auth key comes from the environment only** — never a flag, never a file in the repo, never logged.
- **Capture failure must not fail the request.** Log it and still return a valid Alexa envelope.
- **No signature verification yet.** Do not add it opportunistically; it is a separate phase with its own design.
- `http.Server` always gets an explicit `ReadHeaderTimeout` and `IdleTimeout`. Never ship a bare `http.Server{}`.

## Major dependencies

Use Context7 for up-to-date documentation — do not guess at APIs. Query the Context7 ID before using any unfamiliar or version-sensitive API (Go 1.27 and several of these post-date the knowledge cutoff).

| Library / source | Context7 ID | Notes |
|---|---|---|
| `tailscale.com/tsnet` | `/tailscale/tailscale` | Embedded tailnet node; `ListenFunnel` on 443 |
| `github.com/urfave/cli/v3` | `/urfave/cli` | **The CLI framework. All command line argument handling uses urfave/cli v3** — never `flag`, never a hand-rolled parser. Note v3 is `cli.Command`, not v2's `cli.App` |
| Alexa Skills Kit docs | look up before use | Request/response envelope shapes; web-service hosting requirements |
| GoReleaser | `/websites/goreleaser` | Release/snapshot builds; ldflags inject `main.version`/`commit`/`date` |

Not on Context7: the Go stdlib (`net/http`, `path/filepath`, `log/slog`, `runtime/debug`) — use upstream docs directly.

### CLI conventions

The binary takes **no positional arguments and almost no flags**: everything an
operator sets is an environment variable (see `config.go`), because that is the
one mechanism that behaves identically under systemd, launchd and the Windows
Service Manager. The CLI layer exists for `--help` and `--version`, not as a
second configuration system. Resist adding a flag that duplicates a
`BILLY_*` variable.

`run(args, stdout, stderr) int` stays a pure function of its arguments so it is
testable without a process boundary. urfave/cli's default `ExitErrHandler`
writes to a package global and calls `os.Exit`, so it is replaced with a no-op
and exit-code mapping is done in `run`. Exit codes: `0` success, `1` runtime
failure, `2` usage error.

## Release automation

`release-please.yml` and `release.yml` delegate to the shared `chinmina/.github` reusable workflows on the keyless octo-sts path. Everything except the GitHub release itself is opted out: no npm, no Docker login, no Homebrew cask, no binstaller install script. Artifacts are copied to hosts by hand.

This path depends on octo-sts trust policies named by convention (`release-please-billy`, `release-billy`) existing for the repository. If they do not, the release jobs fail at their first step — substitute a plain local `goreleaser release` job rather than weakening the token model.
