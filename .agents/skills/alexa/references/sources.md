# Sources

## Envelopes, directives, and general behavior

Query Context7 `/alexa/alexa-skills-kit-sdk-for-nodejs` for the specific concept, then compare
the SDK's request-envelope types or response-builder output when documentation prose is
ambiguous. Concepts worth querying directly: `AudioPlayer.Play`, `AudioPlayer.Stop`,
`playBehavior`, stream URL/token/offset, output speech, session-ending behavior,
`RequestEnvelopeUtils`, intent and slot extraction, and
`context.System.device.supportedInterfaces` capability detection.

- Node SDK: <https://github.com/alexa/alexa-skills-kit-sdk-for-nodejs>
- ASK CLI: <https://developer.amazon.com/en-US/docs/alexa/smapi/ask-cli-intro.html>

## Signature verification

Two official verifiers are the normative specification. Implement what both do; where they
differ, accept only the intersection unless current Amazon web-service documentation is
stricter. Port the checks, not the SDKs' language-specific architecture.

| SDK | Repository | Commit | Directory |
| --- | --- | --- | --- |
| Node | `alexa/alexa-skills-kit-sdk-for-nodejs` | `ea88cef4a72a44abf1aff6083472ba08a665862a` | `ask-sdk-express-adapter/lib/verifier/` |
| Java | `alexa/alexa-skills-kit-sdk-for-java` | `e7f16b0523b24a34a7971d4df7ecbb48b4539639` | `ask-sdk-servlet-support/src/com/amazon/ask/servlet/` |

- <https://github.com/alexa/alexa-skills-kit-sdk-for-nodejs/tree/ea88cef4a72a44abf1aff6083472ba08a665862a/ask-sdk-express-adapter/lib/verifier>
- <https://github.com/alexa/alexa-skills-kit-sdk-for-java/tree/e7f16b0523b24a34a7971d4df7ecbb48b4539639/ask-sdk-servlet-support/src/com/amazon/ask/servlet>

The Java SDK has no Context7 entry. Fetch the pinned files transiently from
`raw.githubusercontent.com`; do not vendor them.

## Media serving

- Go `http.ServeContent`: <https://pkg.go.dev/net/http#ServeContent>
- Go `os.Root`: <https://pkg.go.dev/os#Root>

## Worked example in this repository

- `pkg/alexaverify/` — verification implementation and tests.
- `alexa.go`, `handlers.go` — envelope decoding, dispatch, media route.
- `alexa_signature_test.go`, `skillpackage_test.go` — signed-route and skill-package tests.
- `docs/alexa-skill-setup.md` — ASK CLI project and deployment workflow.
