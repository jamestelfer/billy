# Alexa developer console setup

The steps for pointing a custom skill at this service. Everything here is
console configuration — none of it is code.

1. Create a **Custom** skill, **self-hosted** (not Alexa-hosted, not Lambda).
2. Build an interaction model — an invocation name, one custom intent with a
   couple of sample utterances, plus the required built-ins. `interaction-model.json`
   in this directory is a minimal starting point. Build the model.
3. **Build → Endpoint**: set **Service Endpoint Type** to **HTTPS**.
4. Set the Default Region endpoint to `https://<host>.<tailnet>.ts.net/alexa`.
5. For the certificate option choose **"My development endpoint has a
   certificate from a trusted certificate authority."** Tailscale provisions a
   genuine publicly-trusted certificate for the `ts.net` name, so neither the
   self-signed upload nor the wildcard option applies.
6. Leave the skill in **Development** status. Do not submit for certification:
   it would fail, correctly, because the endpoint does not yet verify that
   requests actually come from Alexa.
7. The skill is available on Echo devices registered to the same Amazon
   account as the developer account. Confirm the Echo is on that account.

**Do not enable the AudioPlayer interface yet.** It changes which requests
Alexa sends and adds interaction-model requirements, with no benefit while the
only goal is a request corpus. AudioPlayer lifecycle requests
(`PlaybackStarted`, `PlaybackFailed`, …) cannot be captured at this stage
anyway: they only fire in response to a `Play` directive backed by a real
stream.

## Getting a corpus

Invoke from the console's **Test** tab first — the iteration loop is much
faster than speaking to a device — then from the physical Echo. Aim for at
least a `LaunchRequest` and one `IntentRequest`, which are two distinct
envelope shapes.

Each invocation leaves a `<stem>.body` / `<stem>.json` pair in the capture
directory. Confirm a capture is intact by checking that the body parses and
that its length matches the `Content-Length` recorded in the sidecar:

```
jq . < <stem>.body >/dev/null && echo "parses"
```

> Captured bodies contain a live `apiAccessToken` plus the user and device
> identifiers. Treat them as credentials: never commit one, never paste one
> into an issue, and scrub those fields before any body becomes a fixture.
