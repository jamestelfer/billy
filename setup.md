# Plan: ABS Alexa Skill — Request Capture Slice

> Source PRD: `ABS skill Sketch` (project document). This plan covers **only** the first three phases: repo bootstrap, Funnel reachability, and raw Alexa request capture. Signed URLs, ABS integration, AudioPlayer directives, SQLite state and signature verification are explicitly **out of scope** and will be planned separately once a request corpus exists.

## Purpose of this slice

The end goal is a self-hosted Alexa skill for Audiobookshelf. Before any of that can be built, we need **real Alexa request bodies** captured verbatim from a live Echo, to serve as the corpus for building and testing request signature verification.

So the target end state of this plan is deliberately small:

> Say something to an Echo → a single Go executable, reachable over Tailscale Funnel, writes the exact request bytes to disk and says "captured" back.

Nothing more. No verification, no ABS, no audio.

---

## Architectural decisions

Durable decisions that apply across all phases in this plan and should carry forward into later phases.

- **Language/runtime**: Go 1.27, single static binary, `CGO_ENABLED=0`.
- **Delivery**: a cross-platform Go service shipped as a single self-contained executable. Linux, macOS and Windows on both `amd64` and `arm64` are **firm targets, equally weighted** — none is a secondary port. Windows is the most likely host in practice, so it gets first-class treatment rather than best-effort. Nothing in the code may assume a platform: no POSIX-only syscalls, no hardcoded path separators, no shelling out to platform tools, no filenames that are legal on one OS and rejected on another. Process supervision is left to the host (systemd, launchd, Windows Service Manager or Task Scheduler, or simply a foreground process).
- **Ingress**: `tsnet` embedded directly in the binary via `ListenFunnel`. No Traefik, no second `tailscaled`, no Cloudflare. TLS is terminated by tsnet using the tailnet's Let's Encrypt certificate for the `<host>.<tailnet>.ts.net` name.
- **Funnel port**: `443`. Funnel permits only 443, 8443 and 10000, and the Alexa service requires the skill endpoint on 443.
- **Routes** (locked now, extended later):
  | Path | Method | Purpose |
  |---|---|---|
  | `/alexa` | POST | Skill endpoint. Capture-only in this plan. |
  | `/healthz` | GET | Liveness probe; no auth; returns 200 and nothing sensitive. |
- **Capture storage**: filesystem, not a database. One raw body file plus one JSON metadata sidecar per request. SQLite arrives in a later phase for playback state, not for capture.
- **Tooling floor**: `jamestelfer/boxed` (see below). Same toolchain, same CI shape, same release automation, same conventions.
- **No signature verification in this plan.** The endpoint is deliberately unauthenticated. This is acceptable *only* because the skill stays in Development status, the Funnel hostname is unguessable-ish, and the phase is short-lived. It is a known, time-boxed gap — see Security notes.

## Tooling floor: `jamestelfer/boxed`

Adopt boxed's tooling wholesale as the starting point. Copy and adapt these files rather than inventing a new setup:

| File | Role | Adaptation needed |
|---|---|---|
| `mise.toml` | Pins `go`, `golangci-lint`, `goreleaser`, `just` | Set `go = "1.27"`. Drop the `binstaller` alias and tool. |
| `justfile` | `verify` = `fmt build lint test`; plus `build`, `test`, `lint`, `fmt`, `xbuild` | Take as-is. |
| `.golangci.yaml` | Linter set incl. `gosec`, `bodyclose`, `noctx`, `nilerr`, `errcheck` | Set `run.go: "1.27"`. Otherwise as-is. |
| `.goreleaser.yaml` | Build matrix, ldflags version injection, draft release | Already covers linux/darwin/windows × amd64/arm64 with a `zip` override for Windows. Take the build and archive sections as-is; remove `homebrew_casks` entirely. |
| `.github/workflows/ci.yml` | Test + lint jobs, mise-driven tool versions, SHA-pinned actions | **Needs change.** Boxed runs every job on `ubuntu-latest`. The test job must become an OS matrix across `ubuntu-latest`, `macos-latest` and `windows-latest`. Lint can stay on Linux alone. Keep the `flake` job only if the flake is adopted. |
| `.github/workflows/pr-title.yml` | Conventional Commits enforcement on PR titles | Take as-is. |
| `.github/workflows/release-please.yml`, `release.yml` | Draft release → attest → publish | Take as-is if the `chinmina` shared workflow is available to this repo; otherwise substitute a plain `goreleaser release` job and note the downgrade. |
| `release-please-config.json`, `.release-please-manifest.json` | Conventional-Commit-driven versioning | Reset manifest to `0.1.0`. Drop `extra-files: flake.nix` if no flake. |
| `version.go` | `main.version/commit/date` + `buildVersion()` fallback via `debug.ReadBuildInfo` | Copy near-verbatim; it is generic. |
| `AGENTS.md` | Agent conventions, Go version warning, Context7 dependency table | Rewrite for this project (see Phase 1). |
| `flake.nix` / `flake.lock` | Nix build | **Optional.** See Phase 1 flex zone. |

Conventions inherited from boxed:

- **Conventional Commits** for all commits and PR titles.
- **`just verify` before every commit.** This is the standard quality gate.
- **Tool versions pinned in `mise.toml`**; CI reads them via `mise current`.
- **Third-party GitHub Actions SHA-pinned**, with a `# vN` comment.
- **`permissions:` default-deny** at workflow level, granted per job.

## Normalization notes

The source sketch is prose; requirements below are normalized to EARS with stable IDs.

- Original: "simplest possible go binary that can act as an Alexa skill, be configured in Amazon, hooked up to my Echo" → `R7`, `R8`, `R10`
- Original: "full bodies of requests sent by Amazon so they can act as a test corpus for signing" → `R8`, `R9`
- Original: "embedding Tailscale in the binary" → `R5`, `R6`
- Original: "bootstrapping to a main.go that runs and has CI etc configured is the first e2e phase" → `R1`, `R2`, `R3`, `R4`

### Requirements

| ID | Requirement |
|---|---|
| `R1` | When a developer runs `just verify`, the toolchain shall run format, build, lint and test and exit zero. |
| `R2` | While a pull request is open or a commit lands on `main`, CI shall run the test and lint jobs using the tool versions pinned in `mise.toml`. |
| `R3` | When a `v*` tag is pushed, the release pipeline shall publish self-contained executables for each supported platform and architecture, including `linux/arm64`. |
| `R4` | When the binary is invoked with `--version`, it shall print a non-empty version string. |
| `R5` | When the service starts, it shall join the tailnet using an auth key supplied via the environment and persist node state to a configured directory. |
| `R6` | While the service is running, it shall serve HTTPS on the Tailscale Funnel listener on port 443. |
| `R7` | When a GET request is made to `/healthz` via the public Funnel URL, the service shall respond `200`. |
| `R8` | When a POST request is received at `/alexa`, the service shall persist the request body to durable storage byte-for-byte unmodified. |
| `R9` | When a POST request is received at `/alexa`, the service shall persist the request metadata — including the `Signature-256` and `SignatureCertChainUrl` headers, receipt timestamp, method, path and remote address — alongside the body. |
| `R10` | When a POST request is received at `/alexa`, the service shall respond within Alexa's response timeout with a structurally valid Alexa response envelope. |
| `R11` | If persisting a captured request fails, then the service shall still return a valid Alexa response and shall log the failure. |
| `R12` | The service shall not perform Alexa request signature verification during this plan's phases. |
| `R13` | The capture directory shall be created with owner-only permissions and shall be excluded from version control. |

## P0 baseline and standard quality gate

Standard commands for this project:

```
just verify    # fmt + build + lint + test — the gate
just build     # produces dist/<binary>
just test      # go test ./...
just lint      # golangci-lint run ./...
just xbuild    # goreleaser snapshot across the release matrix
```

- [ ] Run `mise install` to match pinned tool versions
- [ ] Run `just verify` as a P0 baseline before Phase 1 completes; it must pass
- [ ] Re-run `just verify` before marking **every** phase complete
- [ ] Do not start the next phase while this gate is failing

---

## Phase 1: Repo bootstrap on the boxed floor

**EARS requirements**: `R1`, `R2`, `R3`, `R4`

### Why this phase exists

Establish a repository where the toolchain, CI and release automation are known-good *before* any behaviour exists. Every later phase then lands against a green baseline, so a failure is unambiguously the new code rather than the scaffolding. This is the cheapest possible moment to get the boring parts right.

### Locked decisions (non-negotiable)

- Go 1.27, module path `github.com/jamestelfer/<repo>`.
- Tooling copied from `jamestelfer/boxed` per the table above — same `justfile` target names, same lint set, same mise-driven CI version reading.
- Release matrix **must** cover `linux`, `darwin` and `windows` on both `amd64` and `arm64`, minus any combination GoReleaser cannot produce. `linux/arm64` is the one that gets deployed first, but it is not privileged in the build.
- `CGO_ENABLED=0` throughout, so every artifact is a single static executable with no runtime dependencies.
- All filesystem paths built with `path/filepath`, never `path` and never string concatenation with `/`.
- CI must prove the cross-platform claim: the test job runs on Linux, macOS and Windows. A change that only passes on one OS is not done.
- No platform-specific code without a matching implementation for the others. Where behaviour genuinely differs, use build tags with a file per platform, following boxed's own `managed_darwin.go` / `managed_other.go` pattern.
- No Homebrew cask, no binstaller, no npm, no Docker image. This is a private service, not a distributed CLI.
- Conventional Commits enforced on PR titles.
- `AGENTS.md` exists and carries a Go 1.27 warning plus a Context7 dependency table (see below).

### Flex zone (implementation choice allowed)

- Repository name and binary name.
- Whether to adopt `flake.nix` and the corresponding `flake` CI job. It is real ongoing cost (`vendorHash` churn) for a service that ships as a plain executable. Recommend **skipping**; if skipped, also drop `extra-files` from the release-please config.
- Whether `release.yml` uses the shared `chinmina` reusable workflow or a plain local `goreleaser release` job.
- Package layout. Single `main.go` is fine at this size; introduce packages when a second concern appears.
- CLI framework: `urfave/cli/v3` matches boxed, but stdlib `flag` is sufficient for `--version` alone.

### End-to-end behaviour to implement

A binary that builds, passes lint and tests, prints its version, and exits. CI runs green on a pull request. A snapshot release produces artifacts for every platform in the matrix. That is the whole of Phase 1 — there is no service yet.

### AGENTS.md content (required)

Model it on boxed's, with these sections:

1. **Go version warning** — Go 1.27 postdates the model knowledge cutoff. Do not rely on training data for stdlib APIs; fetch current docs via Context7.
2. **Build and test** — the `just` targets and `mise install`.
3. **Project layout**.
4. **Commits and PR titles** — Conventional Commits; note that here they *do* carry version-bump semantics because release-please is wired up (this differs from boxed).
5. **Major dependencies with Context7 IDs** — seed with:

   | Library | Context7 ID | Notes |
   |---|---|---|
   | `tailscale.com/tsnet` | `/tailscale/tailscale` | Embedded node; `ListenFunnel` |
   | Alexa Skills Kit docs | look up before use | Request/response envelope shapes |
   | GoReleaser | `/websites/goreleaser` | ldflags version injection |

### Acceptance criteria

- [ ] `[observable]` `mise install && just verify` exits zero on a clean checkout
- [ ] `[observable]` `./dist/<binary> --version` prints a non-empty string, and still does so for a plain `go build` (no ldflags) via the `debug.ReadBuildInfo` fallback
- [ ] `[observable]` CI test and lint jobs pass on a pull request
- [ ] `[observable]` `just xbuild` produces an artifact for every platform/arch in the matrix, including `linux_arm64`
- [ ] `[observable]` The `linux_arm64` executable runs on the Pi and the host-native executable runs on the development machine, both from a plain copy with nothing installed alongside
- [ ] `[structural]` `mise.toml` pins `go = "1.27"`; `.golangci.yaml` sets `run.go: "1.27"`; `go.mod` declares `go 1.27`
- [ ] `[structural]` `AGENTS.md` exists with all five sections above

### Verification

Clean-clone the repo, run `mise install`, then `just verify` — observe zero exit and no lint findings. Open a throwaway PR and observe both CI jobs green. Run `just xbuild` and confirm `dist/` contains one build per matrix entry. Copy the `linux_arm64` executable to the Pi, run it with `--version`, and observe output with no shared-library errors.

### Replan triggers

- Go 1.27 is not yet available in the mise registry, or `golangci-lint` 2.x does not yet support `go: "1.27"` — pause and decide whether to pin 1.26 instead.
- The shared `chinmina` release workflow is not accessible to this repo.

---

## Phase 2: Public reachability over embedded Funnel

**EARS requirements**: `R5`, `R6`, `R7`

**Carry-forward**: re-run `just verify` and confirm CI is green before starting.

### Why this phase exists

Funnel is the single highest-risk unknown in the whole architecture — auth key handling, tailnet ACL attributes, state persistence across restarts and port constraints all have to line up before Alexa can ever reach the box. Proving public reachability with a trivial handler isolates that risk completely from Alexa's own complexity. If this phase is hard, it is much better to discover that against `curl` than against an Echo.

### Locked decisions (non-negotiable)

- Ingress is `tsnet.Server` + `ListenFunnel` **in-process**. No Traefik, no system `tailscaled`, no reverse proxy in front.
- Listener on port `443`.
- Auth key is read from the environment (`TS_AUTHKEY` or similar), never a flag, never committed. The auth key must be **reusable and non-ephemeral** so node identity survives restarts.
- `tsnet.Server.Dir` points at a persistent directory outside the working directory, owner-only permissions.
- Node identity and hostname are stable: the Funnel URL must not change across restarts, because it will be pinned in the Alexa developer console.
- `/healthz` returns 200 with a fixed, non-sensitive body. It must not leak version, hostname, tailnet name or paths.
- `http.Server` has explicit `ReadHeaderTimeout` and `IdleTimeout` set. Do not ship a bare `http.Server{}`.
- Graceful shutdown on interrupt and termination signals via `signal.NotifyContext` with `os.Interrupt` and `syscall.SIGTERM` — stop accepting, drain, close the tsnet server. `signal.NotifyContext` is portable; do not reach for POSIX-only signal handling.

### Flex zone (implementation choice allowed)

- Deployment mechanism (copying the release artifact, a `just` recipe, Ansible — whatever is least friction). Deployment is host-specific and lives outside the binary.
- Process supervision. On the Pi this will be a systemd unit, which should use an `EnvironmentFile` readable only by the service user, `Restart=on-failure`, and a `StateDirectory`. Equivalent arrangements on other platforms are the operator's problem, not the binary's.
- Default state and capture directory locations. Deriving them from `os.UserConfigDir` / `os.UserCacheDir` keeps them portable; both must be overridable by configuration.
- Router/mux choice: `net/http` `ServeMux` is entirely sufficient.
- Logging library. `log/slog` is the obvious default.

### Open questions / risk burn-down

- **Tailnet ACL**: Funnel requires the `funnel` node attribute for the node's tag or user in the tailnet policy file. Confirm this is granted *before* writing code — it is a console change, not a code change, and it silently fails otherwise. Retire this risk first.
- **Auth key vs. OAuth client**: a reusable auth key expires (90 days max). For a long-lived service this will eventually need re-keying. Note the expiry date; consider a tagged OAuth client later. Not a blocker for this phase.
- **tsnet on Windows**: `tsnet` is pure Go userspace networking and should build and run on all three platforms, but this has not been proven for this project. Verify the Windows path early — it is the most likely host, and discovering a problem here after Phase 3 is written would be expensive. Also confirm the state directory default is sane on Windows.
- **First-run latency**: cert provisioning on first Funnel listen can take several seconds. Confirm the service tolerates this at startup rather than timing out.

### End-to-end behaviour to implement

The binary starts, joins the tailnet as a named node, provisions its Funnel listener on 443, and serves `/healthz`. From a machine with **no** Tailscale installed and on a different network, `curl https://<host>.<tailnet>.ts.net/healthz` returns 200 with a valid publicly-trusted certificate.

### Acceptance criteria

- [ ] `[observable]` `curl -v https://<host>.<tailnet>.ts.net/healthz` from an off-tailnet network returns 200 with no TLS warnings
- [ ] `[observable]` The presented certificate chains to a public CA and its SAN matches the Funnel hostname
- [ ] `[observable]` Stopping and restarting the process yields the same Funnel URL, and `/healthz` recovers without re-authing
- [ ] `[observable]` An interrupt or termination signal shuts the service down cleanly, with no orphaned tailnet node left registered
- [ ] `[structural]` The auth key is sourced from the environment only; no key appears in the repo, in flags, in logs, or in the service definition itself
- [ ] `[structural]` No POSIX-only API is used without a build-tagged equivalent for the other platforms; `just xbuild` still produces the full matrix
- [ ] `[structural]` `ReadHeaderTimeout` and `IdleTimeout` are set explicitly

### Regression watchpoints

- `just verify` still passing — tsnet pulls a large dependency tree and may surface new lint findings, particularly from `gosec` and `noctx`.
- Binary size and build time will jump noticeably. Confirm `just xbuild` still completes for **every** platform in the matrix — `tsnet` is the most likely place for a cross-platform build to break, so a failure here is a signal, not a nuisance to be worked around by trimming the matrix.

### Replan triggers

- Funnel cannot be enabled on the tailnet, or the node attribute cannot be granted.
- `ListenFunnel` proves unreliable across restarts — would force reconsidering the sidecar `tailscaled` + Traefik path that this design deliberately avoided.
- Certificate provisioning requires interactive approval.

---

## Phase 3: Alexa request capture

**EARS requirements**: `R8`, `R9`, `R10`, `R11`, `R12`, `R13`

**Carry-forward**: re-verify Phase 2 — confirm `/healthz` still answers over the public URL after any changes — before starting.

### Why this phase exists

This is the phase that produces the actual deliverable: a corpus of genuine Alexa request bodies. Signature verification cannot be built or tested honestly without real requests to test against, and the exact byte sequence matters because the signature is computed over the raw body. Everything here exists to get those bytes onto disk intact.

### Locked decisions (non-negotiable)

- **The body is persisted byte-for-byte.** Read the raw bytes and write them unmodified. Do **not** unmarshal-then-remarshal, do not reformat, do not normalize line endings, do not strip whitespace. A round-tripped body is worthless for signature testing.
- Two files per request, sharing a stem:
  - `<stem>.body` — raw bytes, exactly as received
  - `<stem>.json` — metadata sidecar
- The stem is time-ordered, collision-free, and **filename-safe on every target platform**. Do not use RFC3339 directly: it contains colons, which Windows rejects outright. Use a colon-free UTC layout such as `20060102T150405.000000000Z` plus a short random suffix.
- Metadata sidecar captures at minimum: receipt time (UTC, nanosecond), method, request URI, protocol, remote address, `Content-Length`, the full header map, and the body length actually read.
- The request body read is **bounded** (`http.MaxBytesReader`). An unbounded read on a public unauthenticated endpoint is a trivial memory exhaustion vector. If the limit is hit, record the truncation explicitly in the sidecar.
- The response is a minimal but structurally valid Alexa envelope: `version`, `response.outputSpeech` (PlainText), `response.shouldEndSession: true`.
- **Capture failure must not fail the request** (`R11`). Log and still respond.
- The capture directory is created restricted to the owner and is gitignored (`R13`). Pass `0700` on creation, but do not rely on it: Go's permission bits are largely inert on Windows. Portable protection comes from **where** the directory lives, not the mode — default it under `os.UserConfigDir` (`%AppData%` on Windows, `~/.config` on Linux, `~/Library/Application Support` on macOS), which is already ACL-restricted to the user on Windows. The location must remain overridable by configuration.
- No signature verification (`R12`). Do not add it "while we're here" — it is a separate phase with its own design.

### Flex zone (implementation choice allowed)

- Sidecar format details beyond the required fields.
- Whether to also fetch and archive the certificate chain from `SignatureCertChainUrl` at capture time. **Recommended but optional** — it costs little and makes offline replay possible while the chain is still valid. It must be best-effort and must never block or fail the response.
- Log format and verbosity.
- Whether the spoken response text is fixed or echoes something (e.g. the intent name) to make console testing easier.

### Open questions / risk burn-down

- **Alexa response timeout.** Commonly cited as ~8 seconds. Disk writes are far inside that, but an optional cert-chain fetch is a network call — if implemented, give it its own short context timeout and never let it gate the response.
- **Interaction model scope.** Keep it minimal: an invocation name, one custom intent with a couple of sample utterances, and the required built-in intents. **Do not enable the AudioPlayer interface yet** — it changes which requests Alexa sends and adds model requirements with no benefit at this stage.
- AudioPlayer lifecycle requests (`PlaybackStarted`, `PlaybackFailed` etc.) cannot be captured in this phase, because they only fire in response to a Play directive backed by a real stream. Capturing those is a later phase and will need this capture handler kept in place.

### Alexa developer console setup

Verified against the Alexa Skills Kit documentation for hosting a custom skill as a web service.

1. Create a **Custom** skill, self-hosted (not Alexa-hosted, not Lambda).
2. Build an interaction model: invocation name, one custom intent with sample utterances, plus the required built-ins. Build the model.
3. Under **Build → Endpoint**, set **Service Endpoint Type** to **HTTPS**.
4. Set the Default Region endpoint to `https://<host>.<tailnet>.ts.net/alexa`.
5. For the certificate option, choose **"My development endpoint has a certificate from a trusted certificate authority."** Tailscale provisions a genuine publicly-trusted certificate for the `ts.net` hostname, so neither the self-signed upload nor the wildcard option applies.
6. Leave the skill in **Development** status. Do not submit for certification — it would fail, correctly, because the endpoint does not yet verify that requests come from Alexa.
7. The skill is available on Echo devices registered to the same Amazon account as the developer account. Confirm the Echo is on that account.

### End-to-end behaviour to implement

Speak the invocation name to the Echo. Alexa POSTs to `/alexa` over Funnel. The handler reads the body, writes `.body` and `.json` to the capture directory, and returns a valid envelope. The Echo speaks the confirmation. The files are on disk with the raw JSON intact.

### Acceptance criteria

- [ ] `[observable]` Speaking the invocation name to a physical Echo produces the spoken confirmation, with no error tone and no "there was a problem"
- [ ] `[observable]` A `.body` / `.json` pair appears in the capture directory for each utterance
- [ ] `[observable]` The `.body` file is byte-identical to what Alexa sent — verify by comparing its SHA-256 and length against the `Content-Length` header recorded in the sidecar
- [ ] `[observable]` The sidecar contains non-empty `Signature-256` and `SignatureCertChainUrl` values
- [ ] `[observable]` A LaunchRequest and at least one IntentRequest are both captured, giving at least two distinct envelope shapes
- [ ] `[observable]` With the capture directory made unwritable, the skill still responds successfully and the failure is logged (`R11`)
- [ ] `[structural]` The handler writes raw bytes with no intermediate unmarshal/remarshal, and the read is bounded by `MaxBytesReader`
- [ ] `[structural]` The capture directory defaults to a per-user location, is created with mode `0700`, and its path is in `.gitignore`
- [ ] `[structural]` Every capture filename is legal on Windows — no colons, no reserved device names, no trailing dots or spaces

### Verification

Invoke the skill from the developer console's Test tab first (faster iteration), then from the physical Echo. On the host, list the capture directory and run `jq . < <stem>.body` to confirm it parses as the JSON Alexa sent. Compare byte length to the recorded `Content-Length`. Then make the directory unwritable (`chmod 0500` on Unix; deny write in the ACL on Windows), invoke again, and confirm the spoken response still succeeds while the log records the write failure.

### Regression watchpoints

- `/healthz` must still answer — the new route must not shadow it.
- Startup must still tolerate first-run cert provisioning latency.

### Replan triggers

- Alexa reports an endpoint error that is not a certificate or reachability problem — investigate before adding workarounds.
- Funnel introduces a body size limit or request timeout that interferes.
- Captured bodies turn out to be transformed in transit in some way that makes byte-exact capture insufficient for signature work.

---

## Security notes (read before starting Phase 3)

These are not optional politeness; two of them are live-credential issues.

1. **The endpoint is unauthenticated for the duration of this plan.** Anyone who learns the Funnel hostname can POST arbitrary bytes and cause disk writes. Mitigations: bounded body reads, an owner-only capture directory, and — importantly — **keep this phase short**. Signature verification is the very next phase. Consider a disk-usage cap or a capture-count limit if the endpoint will be up for more than a few days.

2. **Captured bodies contain live credentials.** Alexa request envelopes include `session.user.userId`, `context.System.device.deviceId`, and `context.System.apiAccessToken` — the last of which is a **real bearer token** for Alexa APIs. Treat the capture directory as secret material: never commit it, never paste bodies into issues or chat logs, and redact before sharing any sample. If a corpus is ever checked in as test fixtures, scrub these fields first.

3. **The Tailscale auth key is a credential.** Environment only, never in the repo, never logged, note its expiry date. How it reaches the environment is platform-specific: a `0600` `EnvironmentFile` under systemd, but on Windows there is no equivalent — a user-scoped environment variable or a DPAPI-protected file, and *not* a plaintext file in the install directory. Decide this per host; the binary only ever reads the environment.

---

## Requirements coverage matrix

| Requirement ID | Phase(s) | Notes |
|---|---|---|
| `R1` | Phase 1 | `just verify` gate |
| `R2` | Phase 1 | CI test + lint jobs |
| `R3` | Phase 1 | Full cross-platform release matrix |
| `R4` | Phase 1 | `--version` incl. build-info fallback |
| `R5` | Phase 2 | tsnet auth key + persistent state dir |
| `R6` | Phase 2 | `ListenFunnel` on 443 |
| `R7` | Phase 2 | `/healthz` |
| `R8` | Phase 3 | Byte-exact body persistence — the core deliverable |
| `R9` | Phase 3 | Metadata sidecar |
| `R10` | Phase 3 | Valid Alexa envelope within timeout |
| `R11` | Phase 3 | Capture failure is non-fatal |
| `R12` | Phase 3 | Explicit scope exclusion; retired by the next plan |
| `R13` | Phase 3 | Owner-only capture directory + gitignored |

**Gaps**: none within this plan's scope. Deliberately deferred to a later plan: Alexa signature verification, HMAC-signed stream URLs, ABS API integration, AudioPlayer directives and lifecycle events, SQLite state, retry handling, and phonetic title matching.

## Suggested next plan (not in scope here)

Once a corpus exists, the natural follow-on phases are:

1. Offline signature verification built and tested against the captured corpus, including a locally generated CA and leaf for testing failure modes without depending on Amazon's certificate lifecycle.
2. Verification enforced on the live endpoint, closing the `R12` gap.
3. ABS integration and the first signed stream URL.
