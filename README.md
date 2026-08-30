# billy

Audiobookshelf skill for Amazon Alexa.

A single self-contained Go executable that joins a tailnet with an embedded
Tailscale node (`tsnet`) and serves the Alexa skill endpoint over Tailscale
Funnel — no reverse proxy, no separate `tailscaled`, no cloud function.

> **Status: early.** The current build reaches the internet over Funnel and
> captures raw Alexa request bodies to disk. That corpus is the input to the
> next phase, which builds request signature verification against it. The
> endpoint is **not yet authenticated** and the skill is expected to stay in
> Development status until it is.

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

```
TS_AUTHKEY=tskey-auth-... ./billy
```

The node's Funnel URL is `https://<hostname>.<tailnet>.ts.net`. `/healthz`
answers `200`; `/alexa` is the skill endpoint.

> **Captured requests are secret material.** Alexa envelopes contain a live
> `apiAccessToken` along with user and device identifiers. The capture
> directory is gitignored and created owner-only; treat its contents as
> credentials.

The tailnet needs a little configuration before any of that works — see
`docs/tailscale-setup.md`, then `docs/alexa-skill-setup.md`.

See `AGENTS.md` for conventions and `setup.md` for the implementation plan.
