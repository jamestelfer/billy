---
name: alexa
description: Build, modify, or review an Alexa custom skill — the HTTPS endpoint, request signature verification, interaction model and manifest, AudioPlayer directives and lifecycle events, audio serving, and ASK CLI deployment. Use this whenever work touches an Alexa skill, an `/alexa` route, `Signature-256`, `SignatureCertChainUrl`, `skill.json`, an interaction model, AudioPlayer, an Echo device, or the `ask` CLI — including when the request sounds like ordinary HTTP handler, JSON envelope, or certificate-validation work.
---

# Alexa custom skills

## Check the sources before writing envelope code

Alexa's wire protocol is split across product documentation, SDK types, and adapter
implementations, and remembered JSON shapes are routinely wrong in ways that type-check and
pass local tests. Query Context7 `/alexa/alexa-skills-kit-sdk-for-nodejs` for the concept you
are implementing, and compare the SDK's envelope types or response-builder output when prose is
ambiguous.

For signature verification, the pinned Node and Java SDK verifiers are the executable
specification: implement what both do, and where they differ accept only the intersection
unless current Amazon documentation is stricter. See
[references/sources.md](references/sources.md) for commits, paths, and Context7 queries.

Some things no local test can establish — whether Amazon's model builder accepts a sample,
whether an utterance routes, whether an Echo can fetch and decode the media. Those need a
development-stage deployment and a physical device, so plan for that boundary rather than
treating green tests as proof.

## Five surfaces, not one envelope

Each of these carries different fields, so decode only what dispatch needs and keep
session-specific fields optional. Code that assumes one shape breaks on the others.

1. **Skill package** — manifest enables interfaces and declares the HTTPS endpoint.
2. **Interaction model** — invocation name, intents, slots, and samples determine routing.
3. **Interactive requests** — `LaunchRequest`, `IntentRequest`; usually carry `session` and
   device capability context.
4. **Lifecycle requests** — asynchronous `AudioPlayer.*` events, often without `session` or
   `intent`.
5. **Media fetches** — plain public HTTPS GET/HEAD. Not signed, not `/alexa`.

## Invariants worth protecting

- **Verify the exact wire bytes.** The signature covers the literal body. Read it once into a
  bounded slice, verify that slice, then decode or capture the same slice. Unmarshalling and
  re-marshalling first destroys the thing being verified.
- **Keep verification unconditional.** Inject roots, clock, and HTTP client for testability; a
  disable flag becomes a production bypass.
- **Never log envelopes, `Signature-256`, `apiAccessToken`, user IDs, or device IDs.** Captured
  envelopes hold live credentials, and editing one to sanitize it invalidates its signature —
  so committed tests need synthetic signed envelopes, not trimmed real ones.
- **Route on intent identity.** An `IntentRequest` reports which intent matched, never which
  sample. Paths needing different authorization need different intents.
- **Let Alexa hold transport state.** `context.AudioPlayer` supplies token, activity, and
  paused offset, which covers same-stream pause/resume/stop. Add storage only for
  cross-session or cross-device resume.
- **Keep stream tokens opaque and stable.** No title, path, credentials, or listener identity;
  unchanged across requests and restarts, since Alexa echoes them back as the stream's identity.
- **Keep media routes unauthenticated and path-fixed.** Alexa fetches them without signature
  headers, so `/alexa` middleware must not cover them — and nothing from the URL, query, or
  headers may become a filesystem path.

## Where to read next

| Task | File |
| --- | --- |
| Signature verification, certificate-URL policy, freshness, rejection logging | [references/request-verification.md](references/request-verification.md) |
| Capability context, intent slots, session attributes | [references/request-envelopes.md](references/request-envelopes.md) |
| Play/Stop/Resume directives, `AudioPlayer.*` lifecycle events | [references/audioplayer.md](references/audioplayer.md) |
| Manifest, built-in intents, slot types, sample utterances | [references/interaction-model.md](references/interaction-model.md) |
| Serving MP3, byte ranges, confining media paths on disk | [references/media-serving.md](references/media-serving.md) |
| Skill-package layout, `ask` commands, deployment | [references/ask-cli-workflow.md](references/ask-cli-workflow.md) |
| Test layers, and what only hardware can confirm | [references/testing.md](references/testing.md) |
| Normative sources, pinned SDK commits, worked example in this repo | [references/sources.md](references/sources.md) |
