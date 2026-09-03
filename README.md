# billy

Audiobookshelf skill for Amazon Alexa.

A single self-contained Go executable that joins a tailnet with an embedded
Tailscale node (`tsnet`) and serves the Alexa skill endpoint over Tailscale
Funnel — no reverse proxy, no separate `tailscaled`, no cloud function.

> **Status: early.** The current build reaches the internet over Funnel,
> verifies every Alexa request in-process, and captures only verified raw
> request bodies. There is no Audiobookshelf integration or AudioPlayer
> behaviour yet; the skill remains in Development status while those land.

## Build

```
mise install   # match the pinned toolchain
just verify    # fmt + build + lint + test
just build     # produces dist/billy
```

Releases ship Linux, macOS and Windows builds for both `amd64` and `arm64`.

## Run

Configuration comes from the environment:

| Variable | Required | Purpose |
|---|---|---|
| `TS_AUTHKEY` | yes, on first run | Reusable, non-ephemeral tailnet auth key |
| `BILLY_HOSTNAME` | no | Tailnet node name (default `billy`) — determines the Funnel URL |
| `BILLY_STATE_DIR` | no | tsnet node state; defaults under the user config dir |
| `BILLY_CAPTURE_DIR` | no | Captured requests; defaults under the user config dir |
| `BILLY_CERT_CHAIN_URL` | no | Public S3 certificate URL used for best-effort startup cache warming |

```
TS_AUTHKEY=tskey-auth-... ./billy
```

The node's Funnel URL is `https://<hostname>.<tailnet>.ts.net`. `/healthz`
answers `200`; `/alexa` is the authenticated skill endpoint. Certificate cache
warming runs asynchronously and never blocks readiness or fails startup; if it
cannot complete, the first valid request performs the same bounded fetch.

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
