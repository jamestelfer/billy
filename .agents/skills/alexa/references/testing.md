# Testing

Layer the tests so each catches something the others cannot:

1. **Model and manifest structure** — invocation name, samples, slot types, required built-ins,
   the AudioPlayer manifest member, endpoint placeholder.
2. **Pure dispatch** — launch, direct match, prompted match, unknown title, unsupported device,
   pause/resume/stop, and every lifecycle event.
3. **JSON snapshots** — complete response envelopes and directive shape, with no real
   deployment hostnames.
4. **Signed route** — generate root → intermediate → leaf certificates, sign the exact
   synthetic request bytes, serve the test chain over TLS, and go through the production router.
5. **Media HTTP** — full GET, HEAD, satisfiable range, unsatisfiable range, fixed-path behavior,
   content type.
6. **Service continuity** — process `PlaybackFailed`, then prove health and later playback still
   work.
7. **Development deployment and device** — model acceptance, utterance routing, public media
   fetch, codec compatibility, audible pause/resume position.

A signed-route test should prove three things at once: the exact body verifies, those same bytes
are what got captured, and dispatch returns the expected response. Pair it with proof that
unsigned, stale, and badly signed requests produce no skill response at all.

## What only hardware settles

Green local tests leave these open, so verify them on a development deployment and a real device
before treating them as done:

- whether the model builder accepts and reliably routes a bare `{title}` `AMAZON.SearchQuery`
  sample for a prompted answer;
- the transport-intent forms and `context.AudioPlayer` values the target Echo actually emits;
- whether the chosen MP3 encoding survives the Echo fetching it through the public endpoint;
- end-to-end pause → resume offset behavior.

When one fails, read the real request and media-access logs to find the failing boundary first.
Broadening the matching, adding persistent state, or redesigning the media server before that
just moves the problem.
