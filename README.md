# billy

Audiobookshelf skill for Amazon Alexa.

A single self-contained Go executable that joins a tailnet with an embedded
Tailscale node (`tsnet`) and serves the Alexa skill endpoint over Tailscale
Funnel — no reverse proxy, no separate `tailscaled`, no cloud function.

> **Status: early.** The current build reaches the internet over Funnel,
> verifies and captures Alexa requests, and can play one externally configured,
> non-sensitive MP3 on an AudioPlayer-capable Alexa device with pause, resume,
> stop, and lifecycle handling. There is no Audiobookshelf integration yet.

## Installation

Pre-built binaries for Linux, macOS, and Windows (amd64 and arm64) are published to
[GitHub Releases](https://github.com/jamestelfer/billy/releases). Every archive
carries a build provenance attestation; see [Verifying releases](#verifying-releases).

<details>
<summary><strong>mise (recommended)</strong></summary>

[mise](https://mise.jdx.dev/) installs from GitHub Releases and verifies the
artifact's checksum and build provenance attestation:

```sh
mise use -g github:jamestelfer/billy
```

</details>

<details>
<summary><strong>Nix</strong></summary>

```sh
nix profile install github:jamestelfer/nixpkgs#billy
```

</details>

<details>
<summary><strong>Manual download</strong></summary>

Download the archive for your platform, verify its provenance, and put the binary on your `PATH`:

```sh
OS=linux ARCH=amd64   # or darwin/windows, arm64
curl -fsSLO "https://github.com/jamestelfer/billy/releases/latest/download/billy_${OS}_${ARCH}.tar.gz"
gh attestation verify "billy_${OS}_${ARCH}.tar.gz" --repo jamestelfer/billy \
  --signer-workflow chinmina/.github/.github/workflows/goreleaser-release.yml
tar -xzf "billy_${OS}_${ARCH}.tar.gz" billy
install -m 0755 billy ~/.local/bin/
```

Windows archives are `.zip`.

</details>

<details>
<summary><strong>go install</strong></summary>

Builds from source take their version from the Go module build info.

```sh
go install github.com/jamestelfer/billy@latest
```

</details>

## Verifying releases

Release archives carry a
[build provenance attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds)
created with [Sigstore](https://www.sigstore.dev/) keyless signing. Each archive
is bound, by digest, to the source commit and the workflow run that built it.
No long-lived signing key exists.

Verify a downloaded archive with the [GitHub CLI](https://cli.github.com/) (2.49.0 or later):

```sh
gh attestation verify billy_linux_amd64.tar.gz --repo jamestelfer/billy \
  --signer-workflow chinmina/.github/.github/workflows/goreleaser-release.yml
```

`--repo` checks the source repository. `--signer-workflow` is required because
the archives are signed by the shared release workflow in `chinmina/.github`,
not by a workflow in this repository.

`checksums.txt` lists every archive. After downloading it alongside an archive, check with
`sha256sum --check --ignore-missing checksums.txt`, or `shasum -a 256 --check --ignore-missing checksums.txt` on macOS.

Releases are immutable: once published, their assets cannot be changed and
their tag cannot be moved. GitHub records a release attestation of the tag,
commit, and every asset. Check a downloaded file against it with GitHub CLI
2.81.0 or later:

```sh
gh release verify-asset v0.2.1 billy_linux_amd64.tar.gz --repo jamestelfer/billy
```

## Build

```
mise install   # match the pinned toolchain
just verify    # fmt + build + lint + test
just build     # produces dist/billy
```

## Run

Configuration comes from the environment:

| Variable | Required | Purpose |
|---|---|---|
| `TS_AUTHKEY` | yes, on first run | Reusable, non-ephemeral tailnet auth key |
| `BILLY_BOOK_DESCRIPTOR` | yes | Path to the external JSON descriptor for the one book to serve |
| `BILLY_HOSTNAME` | no | Tailnet node name (default `billy`) — determines the Funnel URL |
| `BILLY_STATE_DIR` | no | tsnet node state; defaults under the user config dir |
| `BILLY_CAPTURE_DIR` | no | Captured requests; defaults under the user config dir |
| `BILLY_CERT_CHAIN_URL` | no | Public S3 certificate URL used for best-effort startup cache warming |

```
TS_AUTHKEY=tskey-auth-... BILLY_BOOK_DESCRIPTOR=/srv/billy/book.json ./billy
```

The descriptor is strict JSON. Its `mp3` path is relative to the descriptor's
own directory and cannot escape it:

```json
{
  "title": "The configured title",
  "author": "Optional author",
  "mp3": "media/book.mp3"
}
```

The node's Funnel URL is `https://<hostname>.<tailnet>.ts.net`. `/healthz`
answers `200`, `/alexa` is the authenticated skill endpoint, and
`/media/book.mp3` publicly serves only the validated MP3 with byte-range
support. Invalid book configuration prevents the listener from opening.
Certificate cache warming runs asynchronously and never blocks readiness or
fails startup; if it cannot complete, the first valid request performs the
same bounded fetch.

> **Captured requests are secret material.** Alexa envelopes contain a live
> `apiAccessToken` along with user and device identifiers. The capture
> directory is gitignored and created owner-only; treat its contents as
> credentials.

The tailnet needs a little configuration before any of that works — see
`docs/tailscale-setup.md`, then `docs/alexa-skill-setup.md`.

## Skill

The Alexa skill lives in `alexa/` as an ASK CLI project, so the manifest and
interaction model are reviewable files rather than console state:

```
export BILLY_SKILL_ENDPOINT=https://<host>.<tailnet>.ts.net/alexa
just skill-deploy    # create or update the skill
just skill-corpus    # replay scripted utterances to fill the capture directory
just skill-talk      # interactive dialog with the deployed skill
```

See `AGENTS.md` for conventions and `setup.md` for the implementation plan.
