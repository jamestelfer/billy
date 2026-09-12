# AudioPlayer

## Start playback

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

- `REPLACE_ALL` for a single selected item.
- HTTPS URL reachable by Alexa from outside any private network.
- New selection starts at offset `0`.
- Token stable across requests and restarts; no title, author, path, credentials, or listener
  identity in it.
- End the conversational session when handing playback to AudioPlayer.
- Output speech and a Play directive can coexist.

Build the media URL from the incoming endpoint origin as a URL, not by string concatenation.
Assert the HTTPS scheme and fixed route in tests without snapshotting a deployment hostname.

## Stop and pause

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

Do not serialize empty `playBehavior` or `audioItem` on a Stop directive. In a single-stream
implementation, compare the active `context.AudioPlayer` token first so a request naming another
stream cannot control the configured one.

## Resume

Current player state arrives on the interactive request:

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

Resume is another `AudioPlayer.Play` for the same URL and token at the reported offset. Confirm
`playerActivity` is paused and the token matches before issuing it. Same-device, same-stream
resume needs no local persistence.

## Lifecycle events are not intents

Dispatch these before interactive intent handling, since they can arrive with no `session` and
no `intent`:

- `AudioPlayer.PlaybackStarted`
- `AudioPlayer.PlaybackStopped`
- `AudioPlayer.PlaybackFinished`
- `AudioPlayer.PlaybackNearlyFinished`
- `AudioPlayer.PlaybackFailed`

A no-op acknowledgement is `{"version":"1.0","response":{}}`.

`PlaybackNearlyFinished` must not enqueue anything when the product has one item.
`PlaybackFailed` should log a structured category and the opaque token — not the raw error body
or envelope — acknowledge successfully, and leave the service available.

Lifecycle events carry `request.token`, `request.offsetInMilliseconds`, and for failures
`request.error.type` and `request.error.message`. Transport intents differ: their playback state
lives under `context.AudioPlayer`.
