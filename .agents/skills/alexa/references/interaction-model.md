# Interaction model and manifest

AudioPlayer needs both the manifest interface and the built-in transport intents.

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

Transport intents to declare:

- `AMAZON.PauseIntent`, `AMAZON.ResumeIntent`, `AMAZON.StartOverIntent`
- `AMAZON.NextIntent`, `AMAZON.PreviousIntent`, `AMAZON.RepeatIntent`
- `AMAZON.LoopOnIntent`, `AMAZON.LoopOffIntent`
- `AMAZON.ShuffleOnIntent`, `AMAZON.ShuffleOffIntent`

Plus the usual custom-skill built-ins: Stop, Cancel, Help, NavigateHome, Fallback. Built-in
intents take no custom samples.

A broad title slot can use `AMAZON.SearchQuery`, but well-formed JSON proves neither model
validity nor routing. A sample consisting only of `{title}` in particular needs testing against
Amazon's development model builder and a real device.

Keep the invocation name and example phrases synchronized with the model. Example phrases
include the wake word; intent samples include neither wake word nor invocation name. Samples
often need both `play {title}` and `to play {title}` so "ask <skill> to play ..." routes.
