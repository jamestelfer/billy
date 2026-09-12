# ASK CLI workflow

Keep the skill package in source control rather than editing in the developer console:

```text
alexa/
  ask-resources.json
  skill-package/skill.json
  skill-package/interactionModels/custom/en-US.json
  dialog/corpus.json
  dist/                         generated, ignored
  .ask/ask-states.json          generated account/skill state, ignored
```

Keep deployment endpoint URLs out of tracked `skill.json` — hold a placeholder there and render
it into an ignored copy before `ask deploy`.

```sh
ask configure
ask deploy
ask dialog --locale en-US --stage development
ask smapi get-skill-status --skill-id <id>
ask smapi get-interaction-model --skill-id <id> --stage development --locale en-US
```

`ask dialog` iterates request/response quickly but cannot replace a physical Echo: device
capability context, media fetching, playback transitions, and spoken routing need device proof.
Saved dialog traffic contains live identity and access-token data — keep it ignored and delete
it after use.
