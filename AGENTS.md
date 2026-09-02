# billy

A self-hosted Alexa skill endpoint for [Audiobookshelf](https://www.audiobookshelf.org/), shipped as a single static executable with Tailscale Funnel ingress embedded via `tsnet`.

**Current scope**: reach the box from the public internet over Funnel, verify that each request really came from Alexa, and capture the raw bytes of every verified request to disk. Signature verification is **partially landed** — see the note under Key conventions. There is no ABS integration and no AudioPlayer support yet; those are separate, later plans. See `setup.md` for the original plan and the signing plan in the wiki for the verification phases.

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
main.go        entry point: urfave/cli command, config, tsnet Funnel listener, graceful shutdown
version.go     build metadata + buildVersion() fallback via debug.ReadBuildInfo
config.go      configuration resolved from the environment
server.go      http.Server construction, serve loop and drain, decoupled from tsnet
handlers.go    route table; /healthz
alexa.go       /alexa: bounded read, verification gate, capture, response envelope
capture.go     byte-exact request capture to disk (body + JSON metadata sidecar)

pkg/alexaverify/   PUBLIC package: Alexa request verification. Its exported
                   surface is a semver contract — a breaking change to it is a
                   `!` major bump. `main` consumes it as an ordinary external
                   consumer, which is the cheapest continuous proof that the
                   API is usable.
  doc.go           package docs: no disable switch, no revocation checking
  verify.go        Verifier, New, Option, Verify, Warm
  errors.go        exported sentinel errors (every failure wraps exactly one)
  url.go           cert chain URL normalization + validation
  timestamp.go     freshness arithmetic
  constants.go     every magic value, named after its Java counterpart

alexa/         ASK CLI project: skill manifest, interaction model, dialog corpus
docs/          Tailscale and Alexa skill setup
NOTICE         attribution for the Apache-2.0 Alexa SDK sources ported from
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
- **The body is persisted byte-for-byte.** Never unmarshal-then-remarshal, reformat, or normalise a captured body: the Alexa signature is computed over the raw bytes, so a round-tripped body is worthless as a corpus — and, now that verification is in the path, a re-encoded body cannot verify at all. The body is read once into a `[]byte`, verified against exactly those bytes, and decoded from the same buffer. Nothing in the handler chain may read, buffer or re-encode it first.
- **Only verified requests are captured.** Capturing rejected ones would let any unauthenticated caller fill the disk and would poison the signed-request corpus. Rejections get a distinct warn line and a running counter instead.
- **The Tailscale auth key comes from the environment only** — never a flag, never a file in the repo, never logged.
- **Capture failure must not fail the request.** Log it and still return a valid Alexa envelope.
- **Signature verification is being landed in phases, and the gate fails closed.** The timestamp and certificate-URL checks are done; chain of trust and the signature itself are not. Until they are, `/alexa` rejects **every** request with `400` — that is intended, not a regression. Follow the signing plan; do not improvise the remaining steps.
- **There is no way to disable verification**, and none may be added — no flag, no environment variable, no build tag. `pkg/alexaverify` exposes exactly three test seams (trusted roots, clock, HTTP client), none of which can weaken production behaviour. Note that a verifier *interface* in the handler would be a disable switch by another name; `main` holds the concrete type deliberately.
- **Certificate revocation is deliberately not checked.** Neither reference SDK checks it either. Documented in `pkg/alexaverify/doc.go` so it is not mistaken for an oversight.
- `http.Server` always gets an explicit `ReadHeaderTimeout` and `IdleTimeout`. Never ship a bare `http.Server{}`.

## Major dependencies

Use Context7 for up-to-date documentation — do not guess at APIs. Query the Context7 ID before using any unfamiliar or version-sensitive API (Go 1.27 and several of these post-date the knowledge cutoff). Every ID below has been resolved and confirmed to exist; do not invent one.

| Library / source | Context7 ID | Notes |
|---|---|---|
| `tailscale.com/tsnet` | `/tailscale/tailscale` | Embedded tailnet node; `ListenFunnel` on 443. Use this one for the **Go API** |
| Tailscale product docs | `/websites/tailscale` | Use this one for **Funnel, ACLs, auth keys and admin console** behaviour — far broader coverage than the repo ID above |
| `github.com/urfave/cli/v3` | `/urfave/cli` | **The CLI framework. All command line argument handling uses urfave/cli v3** — never `flag`, never a hand-rolled parser. Note v3 is `cli.Command`, not v2's `cli.App`, so most v2 answers do not transfer |
| Alexa Skills Kit SDK for Node.js | `/alexa/alexa-skills-kit-sdk-for-nodejs` | One of the two normative sources for request verification; also request/response envelope shapes |
| GoReleaser | `/goreleaser/goreleaser` | Release/snapshot builds; ldflags inject `main.version`/`commit`/`date` |

Not on Context7 — go to the source instead:

- **The Go stdlib** (`net/http`, `crypto/x509`, `crypto/rsa`, `path/filepath`, `log/slog`, `runtime/debug`): use upstream docs directly. Watch for Go 1.27 idioms the linter enforces, such as `errors.AsType[T](err)` over `errors.As(err, &target)`.
- **The Alexa Skills Kit SDK for Java.** It is the *other* normative source for signature verification and has no Context7 entry. Fetch the pinned files transiently from `raw.githubusercontent.com`; do **not** vendor them.

### Pinned Alexa verification sources

`pkg/alexaverify` is a port of the request verification in two of Amazon's own SDKs. Both trees are pinned, both are Apache-2.0, and attribution is in `NOTICE`. Fetch them when working on that package; never paraphrase from memory.

| Source | Repo | Commit | Path |
|---|---|---|---|
| Java | `alexa/alexa-skills-kit-sdk-for-java` | `e7f16b0523b24a34a7971d4df7ecbb48b4539639` | `ask-sdk-servlet-support/src/com/amazon/ask/servlet/` |
| Node | `alexa/alexa-skills-kit-sdk-for-nodejs` | `ea88cef4a72a44abf1aff6083472ba08a665862a` | `ask-sdk-express-adapter/lib/verifier/` |

**The intersection principle** is the standing adjudication rule: Amazon cannot emit anything either SDK rejects, or every skill running that SDK would break on the next request. So where the two agree, that is the spec; where they disagree, take the stricter behaviour at zero compatibility risk. Rejecting something *both* accept leaves the intersection and needs a named sentinel error, a distinct warn log and a residual-risk register entry — currently only two: cert-URL userinfo and percent-encoded dot segments.

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
