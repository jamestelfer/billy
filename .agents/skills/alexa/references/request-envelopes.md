# Request envelopes

## Capability detection

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

Check for a non-null `AudioPlayer` member. Intent name and device ID prove nothing about
capability. A playback request from an unsupported device should explain the limitation and emit
no Play directive.

## Intent slots

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

The raw slot `value` is enough for deterministic application-side matching. Entity-resolution
data is optional and nested considerably deeper; require it only if the product needs catalog
resolution.

## Session attributes

Return `sessionAttributes` at the top level of the response envelope and set
`response.shouldEndSession` to `false`. The next interactive request returns those values under
`session.attributes`. Keep the state minimal and non-sensitive:

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

## Intent identity, not utterance identity

An `IntentRequest` names the matched intent and nothing about the matched sample. When direct
playback and a prompted bare-title answer need different authorization rules, give them separate
intent names and gate only the prompted one on the session attribute. Reconstructing the matched
sample from the slot value is guesswork.
