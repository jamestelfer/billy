---
name: alexa

description: Draft reference notes for agents implementing or reviewing an Alexa custom-skill HTTPS endpoint, especially request verification, ASK interaction models, AudioPlayer directives, media delivery, sessions, transport controls, lifecycle events, and realistic testing.
---

# Alexa custom skills: practical implementation notes

This is source material for a future polished skill. It records behavior and pitfalls learned while implementing a self-hosted Alexa custom-skill endpoint. Treat the references below as authoritative over this summary, and re-check current documentation before relying on a version-sensitive detail.

## Authority and source strategy

Alexa's wire protocol is spread across product documentation, SDK types/helpers, and adapter implementations. Do not design from remembered JSON shapes.

For ordinary request/response and AudioPlayer work:

1. Check current Alexa documentation and SDK examples.
2. Query Context7 library `/alexa/alexa-skills-kit-sdk-for-nodejs` for the specific concept.
3. Compare the SDK's request-envelope types or response builder output when prose is ambiguous.
4. Confirm uncertain interaction-model behavior by deploying to the development stage. Local JSON tests cannot prove that Amazon's model builder accepts or routes an utterance as expected.

For HTTPS request signature verification, use both pinned official SDK implementations as executable specifications:

- Node SDK commit `ea88cef4a72a44abf1aff6083472ba08a665862a`, directory `ask-sdk-express-adapter/lib/verifier/`
- Java SDK commit `e7f16b0523b24a34a7971d4df7ecbb48b4539639`, directory `ask-sdk-servlet-support/src/com/amazon/ask/servlet/`

A useful adjudication rule is: where both SDKs agree, implement that behavior; where they disagree, accept only the intersection unless current Amazon web-service documentation is stricter. Port the checks, not language-specific SDK architecture.

## The useful mental model

A custom skill has several independently important surfaces:

1. **Skill package**: the manifest enables interfaces and declares the HTTPS endpoint.
2. **Interaction model**: invocation name, intents, slots, and sample utterances determine routing.
3. **Interactive requests**: `LaunchRequest` and `IntentRequest`, usually carrying `session` and device capability context.
4. **AudioPlayer lifecycle requests**: asynchronous `AudioPlayer.*` events, which may omit normal interactive-session fields.
5. **Media fetches**: separate public HTTPS GET/HEAD requests. They are not Alexa-signed `/alexa` requests.

Do not force all of these through one assumed envelope shape. Decode only fields needed for dispatch and make session-specific fields optional.

## Key lessons from the Static Play implementation

These are the reusable findings recorded in the task's progress tracker:

- **Reuse the verified bytes.** Playback dispatch can decode the same byte slice used by verification and capture. There is no need to reread or re-encode the body, and doing so would weaken the exact-byte signature invariant.
- **Use intent identity to distinguish conversational paths.** Direct and prompted title turns need separate intents because an `IntentRequest` says which intent matched, not which sample matched. A small session attribute can then authorize only the prompted-title intent.
- **Let Alexa own transient transport state.** For same-stream pause/resume/stop, `context.AudioPlayer` supplies the stable token, player activity, and paused offset. Do not introduce a database or listener-scoped progress state unless the product requires cross-session or cross-device resume.
- **Treat cross-platform build time as tooling behavior, not Alexa behavior.** A first cross-platform build may recompile the full dependency tree and take substantially longer; later builds are usually cached. Existing CI/build-matrix checks can carry this concern while feature work stays focused on Alexa behavior.

The baseline and completed implementation remained green under the repository's standard `just verify` gate; the remaining uncertainty is at the Amazon deployment and physical-device boundaries, not in the local envelope and HTTP tests.

## HTTPS request verification

### Preserve the wire bytes

The signature covers the exact HTTP request-body bytes, not semantically equivalent JSON.

- Read the body once into a bounded byte slice.
- Verify that exact slice.
- Decode and capture that same slice only after verification.
- Never unmarshal and re-marshal before verification.
- A truncated or partially read body must fail closed; do not verify its prefix.

A safe order is:

1. Read the bounded body completely.
2. Check timestamp freshness.
3. Validate and fetch the signing certificate chain.
4. Validate the chain and certificate hostname.
5. Verify the signature.
6. Only then capture and dispatch.

Checking freshness before certificate retrieval prevents stale unauthenticated traffic from causing outbound fetches.

### Relevant headers and cryptography

Alexa supplies:

- `Signature-256`
- `SignatureCertChainUrl`

The implemented modern path verifies RSA PKCS#1 v1.5 with SHA-256. Missing headers, malformed certificate URLs, untrusted chains, wrong certificate hostnames, unsupported key types, and bad signatures should all fail before skill behavior.

Certificate URL handling is security-sensitive because caller input selects an outbound fetch. The pinned SDK comparison produced this practical policy:

- Require HTTPS.
- Require host `s3.amazonaws.com`, case-insensitively.
- Permit no explicit port other than `443`.
- Normalize the decoded path before checking it.
- Require the normalized path to begin with `/echo.api/`, case-sensitively.
- Strip fragments.
- Reject userinfo.
- Reject encoded or ordinary traversal that escapes the allowed prefix.

Validate the leaf through supplied intermediates to system roots, then require DNS SAN `echo-api.amazon.com`. Verify validity dates on every cache hit, not only when first fetching the chain. Keep network fetches bounded by timeout, retry count, and response-size limits.

The default request freshness tolerance used here is 150 seconds in both past and future directions. `AlexaSkillEvent.*` events use a separate one-hour allowance; do not assume that means all `AudioPlayer.*` events get the same allowance.

Do not expose a production switch that disables verification. Testability should come from injecting roots, clock, and HTTP client—not bypassing the security boundary.

### Rejection and logging behavior

Return one indistinguishable client response for verification failures while logging stable internal categories. Do not return “timestamp valid but signature bad” detail to a caller.

Never log:

- request bodies;
- `Signature-256` values;
- `apiAccessToken`;
- user IDs;
- device IDs.

Real captured envelopes contain live bearer tokens and identifiers. Do not commit, paste, or lightly “sanitize” a signed capture: editing it invalidates its signature. Use generated certificates and synthetic signed envelopes for committed tests.

## Request-envelope details that matter

### Capability detection

Audio playback support is advertised under:

```json
{
  "context": {
    "System": {
      "device": {
        "supportedInterfaces": {
          "AudioPlayer": {}
        }
      }
    }
  }
}
```

Check for a non-null `AudioPlayer` member. Do not infer support from the intent name or device ID. A matching playback request on an unsupported device should explain the limitation and emit no Play directive.

### Intent slots

A custom intent arrives approximately as:

```json
{
  "request": {
    "type": "IntentRequest",
    "intent": {
      "name": "PlayBookIntent",
      "slots": {
        "title": {
          "name": "title",
          "value": "the spoken title"
        }
      }
    }
  }
}
```

The raw slot `value` is enough for deterministic application matching. Entity-resolution data is optional and has a deeper nested shape; do not require it unless the product actually needs catalog resolution.

### Session attributes

To continue a conversational turn, return `sessionAttributes` at the top level of the response envelope and set `response.shouldEndSession` to `false`. The next interactive request returns those values under `session.attributes`.

Keep session state minimal and non-sensitive. For example:

```json
{
  "version": "1.0",
  "sessionAttributes": {
    "awaitingTitle": true
  },
  "response": {
    "outputSpeech": {
      "type": "PlainText",
      "text": "What would you like to play?"
    },
    "reprompt": {
      "outputSpeech": {
        "type": "PlainText",
        "text": "What would you like to play?"
      }
    },
    "shouldEndSession": false
  }
}
```

An `IntentRequest` identifies the selected intent but does not tell the endpoint which sample utterance matched it. If direct playback and a prompted bare-title answer need different authorization/state rules, use separate intent names. Then gate only the prompted intent on the session attribute. Do not rely on reconstructing the matched sample from the slot value.

## Interaction-model lessons

Enabling AudioPlayer requires both the manifest interface and the appropriate built-in intents.

Manifest fragment:

```json
{
  "manifest": {
    "apis": {
      "custom": {
        "endpoint": {
          "uri": "https://example.invalid/alexa",
          "sslCertificateType": "Trusted"
        }
      },
      "audioPlayer": {}
    }
  }
}
```

The model used during this work includes:

- `AMAZON.PauseIntent`
- `AMAZON.ResumeIntent`
- `AMAZON.LoopOffIntent`
- `AMAZON.LoopOnIntent`
- `AMAZON.NextIntent`
- `AMAZON.PreviousIntent`
- `AMAZON.RepeatIntent`
- `AMAZON.ShuffleOffIntent`
- `AMAZON.ShuffleOnIntent`
- `AMAZON.StartOverIntent`
- the usual custom-skill built-ins such as Stop, Cancel, Help, NavigateHome, and Fallback

Built-in intents have no custom samples. A broad title slot can use `AMAZON.SearchQuery`, but model acceptance and routing—especially a sample consisting only of `{title}`—must be tested against Amazon's development model builder and a real device. JSON being well-formed is not proof that the model is valid or that speech will route as expected.

Keep the invocation name and examples synchronized with the model. Example phrases include the wake word; intent samples do not include the wake word or invocation name. Depending on phrasing, samples may need both forms such as `play {title}` and `to play {title}` so “ask <skill> to play ...” routes correctly.

## AudioPlayer responses

### Start playback

A successful selection returns an `AudioPlayer.Play` directive:

```json
{
  "version": "1.0",
  "response": {
    "outputSpeech": {
      "type": "PlainText",
      "text": "Playing the configured title."
    },
    "directives": [
      {
        "type": "AudioPlayer.Play",
        "playBehavior": "REPLACE_ALL",
        "audioItem": {
          "stream": {
            "token": "stable-opaque-token",
            "url": "https://public.example/media/book.mp3",
            "offsetInMilliseconds": 0
          }
        }
      }
    ],
    "shouldEndSession": true
  }
}
```

Practical invariants:

- Use `REPLACE_ALL` for a single selected item.
- The URL must be HTTPS and reachable by Alexa outside any private network.
- Start new selection at offset `0`.
- Keep the token stable across requests and restarts.
- Keep title, author, local path, credentials, and listener identity out of the token.
- End the conversational session when handing playback to AudioPlayer.
- Output speech and a Play directive can coexist; the SDK response-builder example does this.

When deriving the media URL from the incoming endpoint origin, construct it as a URL rather than concatenating untrusted strings. Test the HTTPS scheme and fixed route without snapshotting a real deployment hostname.

### Stop playback

Pause and stop use:

```json
{
  "version": "1.0",
  "response": {
    "directives": [
      { "type": "AudioPlayer.Stop" }
    ]
  }
}
```

Do not serialize empty `playBehavior` or `audioItem` fields on a Stop directive. In a one-stream implementation, compare the active context token before acting so a request for another stream cannot control the configured stream.

### Resume playback

The interactive request can carry current player state under `context.AudioPlayer`:

```json
{
  "context": {
    "AudioPlayer": {
      "playerActivity": "PAUSED",
      "token": "stable-opaque-token",
      "offsetInMilliseconds": 42000
    }
  }
}
```

Resume is another `AudioPlayer.Play` directive for the same URL and token, with the reported non-zero offset. It does not require inventing local persistence if the scope is same-device/same-stream resume. Confirm `playerActivity` is paused and the token is the configured stream before issuing Play.

## Lifecycle events are not intents

Handle these request types before interactive intent dispatch:

- `AudioPlayer.PlaybackStarted`
- `AudioPlayer.PlaybackStopped`
- `AudioPlayer.PlaybackFinished`
- `AudioPlayer.PlaybackNearlyFinished`
- `AudioPlayer.PlaybackFailed`

They can arrive without `session` or `intent`. A safe acknowledgement for a no-op lifecycle event is:

```json
{"version":"1.0","response":{}}
```

Do not let `PlaybackNearlyFinished` accidentally enqueue another item when the product has only one item. `PlaybackFailed` should log a structured category and opaque token, return a successful acknowledgement, and leave the service available. Avoid logging the raw error body or request envelope.

The lifecycle event itself carries fields such as `request.token`, `request.offsetInMilliseconds`, and—for failures—`request.error.type` and `request.error.message`. These differ from transport intents, whose current playback state is under `context.AudioPlayer`.

## Serving audio in practice

The media request is a separate HTTP transaction and does not carry the Alexa request signature headers. Authenticate `/alexa`; do not accidentally apply that middleware to the media route.

For a static MP3 endpoint:

- serve `Content-Type: audio/mpeg` explicitly;
- support GET and HEAD;
- support byte ranges and return coherent `206`, `Content-Range`, and `Content-Length` values;
- return `416` for an unsatisfiable range;
- keep the route fixed rather than converting URL/query/header input into a filesystem path;
- use a publicly reachable HTTPS certificate trusted by Alexa.

Go's `http.ServeContent` supplies range and conditional-request handling when given an `io.ReadSeeker`; set `Content-Type` before calling it. A physical Echo remains the decisive test of URL reachability and MP3 encoding compatibility.

If media is configured from disk, validate it before opening the public listener. Resolve it relative to an operator-controlled descriptor directory and prevent lexical and symlink escape. Go's `os.Root` is a useful confinement primitive: it follows links within the root but rejects links that resolve outside it.

## ASK CLI workflow lessons

Keep the skill package in source control rather than editing only in the developer console. A useful layout is:

```text
alexa/
  ask-resources.json
  skill-package/skill.json
  skill-package/interactionModels/custom/en-US.json
  dialog/corpus.json
  dist/                         generated, ignored
  .ask/ask-states.json          generated account/skill state, ignored
```

Keep deployment-specific endpoint URLs out of tracked `skill.json`. Render a placeholder into an ignored copy before `ask deploy`.

Useful commands:

```sh
ask configure
ask deploy
ask dialog --locale en-US --stage development
ask smapi get-skill-status --skill-id <id>
ask smapi get-interaction-model --skill-id <id> --stage development --locale en-US
```

`ask dialog` is valuable for request/response iteration, but it is not a replacement for a physical Echo. Device capability context, media fetching, playback transitions, and spoken routing need device proof. Saved dialog traffic can contain live identity and access-token data; keep it ignored and delete it after use.

## Testing strategy that worked

Use several layers:

1. **Model/manifest structural tests**: invocation name, samples, slot types, required built-ins, AudioPlayer manifest member, endpoint placeholder.
2. **Pure dispatch tests**: launch, direct match, prompted match, unknown title, unsupported device, pause/resume/stop, and every lifecycle event.
3. **JSON snapshots**: complete response envelopes and directive shape. Avoid real deployment hostnames.
4. **Signed route tests**: generate root → intermediate → leaf certificates, sign the exact synthetic request bytes, fetch the test chain over TLS, and pass through the production router.
5. **Media HTTP tests**: full GET, HEAD, satisfiable range, unsatisfiable range, fixed-path behavior, and content type.
6. **Service-continuity tests**: process `PlaybackFailed`, then prove health and later playback still work.
7. **Development deployment and physical-device checks**: model acceptance, utterance routing, public media fetch, codec compatibility, and audible pause/resume position.

A signed route test should prove all three outcomes together: the exact body verifies, those same bytes are captured, and dispatch returns the expected response. Also prove unsigned/stale/badly signed requests produce no skill response.

## Known unproven points from this implementation session

Do not present these as settled until live verification is done:

- Whether Amazon's current model builder accepts and reliably routes a bare `{title}` `AMAZON.SearchQuery` sample for a prompted answer.
- The exact transport-intent forms and `context.AudioPlayer` values emitted by the target physical Echo.
- Whether the chosen MP3 encoding is accepted after the Echo fetches it through the public endpoint.
- End-to-end pause → resume offset behavior on hardware.

If one of these fails, first inspect the real, securely stored request and media-access logs. Do not broaden matching, add persistent state, or redesign the media server until the failing boundary is identified.

## References used

### Current SDK documentation

- Context7 library: `/alexa/alexa-skills-kit-sdk-for-nodejs`
  - Queried for `AudioPlayer.Play`, `AudioPlayer.Stop`, `playBehavior`, stream URL/token/offset, output speech, and session-ending behavior.
  - Queried for `RequestEnvelopeUtils`, intent/slot extraction, and `context.System.device.supportedInterfaces` capability detection.
- Alexa Skills Kit SDK for Node.js repository: <https://github.com/alexa/alexa-skills-kit-sdk-for-nodejs>
- ASK CLI introduction: <https://developer.amazon.com/en-US/docs/alexa/smapi/ask-cli-intro.html>

### Pinned request-verification sources

- Node verifier at commit `ea88cef4a72a44abf1aff6083472ba08a665862a`: <https://github.com/alexa/alexa-skills-kit-sdk-for-nodejs/tree/ea88cef4a72a44abf1aff6083472ba08a665862a/ask-sdk-express-adapter/lib/verifier>
- Java servlet verifier at commit `e7f16b0523b24a34a7971d4df7ecbb48b4539639`: <https://github.com/alexa/alexa-skills-kit-sdk-for-java/tree/e7f16b0523b24a34a7971d4df7ecbb48b4539639/ask-sdk-servlet-support/src/com/amazon/ask/servlet>

### Media-serving references

- Go `http.ServeContent`: <https://pkg.go.dev/net/http#ServeContent>
- Go `os.Root`: <https://pkg.go.dev/os#Root>

### Repository case study

- `.wiki-sync/signing.plan-1.md` — source adjudication, signature-verification behavior, and threat model.
- `.wiki-sync/static.plan-1.md` — AudioPlayer, media, conversation, transport, and lifecycle requirements.
- `docs/alexa-skill-setup.md` — ASK CLI project and deployment workflow.
- `pkg/alexaverify/` — concrete Go verification implementation and tests.
- `alexa.go`, `handlers.go`, `alexa_signature_test.go`, and `skillpackage_test.go` — practical envelope, routing, media, and integration examples.
