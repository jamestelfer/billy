# Alexa skill setup

The skill is an [ASK CLI](https://developer.amazon.com/en-US/docs/alexa/smapi/ask-cli-intro.html)
project under [`alexa/`](../alexa): the manifest and the interaction model are
files in this repository, and `just` recipes create, deploy and exercise the
skill. Nothing here needs the developer console.

The endpoint has to be reachable before the skill can point at it: do
[`tailscale-setup.md`](tailscale-setup.md) first, and confirm `/healthz`
answers over the public Funnel URL.

## What is in the project

```
alexa/
  ask-resources.json                        which skill package to deploy
  skill-package/
    skill.json                              manifest: endpoint, publishing info
    interactionModels/custom/en-US.json     invocation name, intents, utterances
  dialog/corpus.json                        scripted utterances for the replay
  dist/                                     rendered package (gitignored)
  .ask/ask-states.json                      skill id, written by ask deploy
```

The interaction model is the minimum this phase needs: the `audiobookshelf`
invocation name, one slot-free custom intent (`CaptureIntent`) with a few
sample utterances, and the built-ins (`AMAZON.CancelIntent`,
`AMAZON.HelpIntent`, `AMAZON.StopIntent`, `AMAZON.NavigateHomeIntent`,
`AMAZON.FallbackIntent`). `skillpackage_test.go` holds both files to the rules
the console enforces, so `just test` catches the mistakes that would otherwise
only surface as a failed import.

**`skill.json` does not contain the endpoint.** The Funnel URL carries the
tailnet name and varies per deployment, so the manifest holds the placeholder
`@BILLY_SKILL_ENDPOINT@` and `just skill-render` substitutes the environment
variable of the same name into `alexa/dist/`. That rendered copy is what gets
uploaded.

## One-time setup

```
npm install -g ask-cli
ask configure          # browser login to the Amazon developer account
```

`ask configure` also offers to link an AWS profile. Decline it — the skill is
self-hosted, there is no Lambda, and `ask-resources.json` declares no
`skillInfrastructure`, so `ask deploy` only ever uploads the skill package.

The Echo has to be registered to the same Amazon account as the developer
account. Confirm that before wondering why the skill is not on the device.

## Create and deploy

```
export BILLY_SKILL_ENDPOINT=https://<host>.<tailnet>.ts.net/alexa
just skill-deploy
```

The first run creates the skill and writes its id to `alexa/.ask/ask-states.json`
(gitignored); later runs update it in place. `just skill-deploy` also enables
the skill for testing on the account, so there is no separate console step.

`ask deploy` skips the upload when the package has not changed. Force it with
`just skill-deploy --ignore-hash`.

`sslCertificateType` is `Trusted`: Tailscale provisions a genuine
publicly-trusted certificate for the `ts.net` name, so neither the self-signed
upload nor the wildcard option applies.

The skill stays in **Development**. Do not submit it for certification: it
would fail, correctly, because the endpoint does not yet verify that requests
come from Alexa. (Certification would also want the icons the manifest omits.)

**Do not enable the AudioPlayer interface yet.** It changes which requests
Alexa sends and adds interaction-model requirements, with no benefit while the
only goal is a request corpus. AudioPlayer lifecycle requests
(`PlaybackStarted`, `PlaybackFailed`, …) cannot be captured at this stage
anyway: they only fire in response to a `Play` directive backed by a real
stream.

## Getting a corpus

```
just skill-corpus      # replays alexa/dialog/corpus.json
just skill-talk        # interactive; .quit to exit, .record to save a replay
```

Both drive real requests at `/alexa` over Funnel, so both leave captures on
disk — much faster than speaking to a device. The scripted corpus covers a
`LaunchRequest`, two `CaptureIntent` utterances and one deliberate nonsense
phrase that lands on `AMAZON.FallbackIntent`, which is three distinct envelope
shapes. Finish with the physical Echo: a real device sends context a
simulation does not.

Each invocation leaves a `<stem>.body` / `<stem>.json` pair in the capture
directory. Confirm a capture is intact by checking that the body parses and
that its length matches the `Content-Length` recorded in the sidecar:

```
jq . < <stem>.body >/dev/null && echo "parses"
```

> Captured bodies contain a live `apiAccessToken` plus the user and device
> identifiers. Treat them as credentials: never commit one, never paste one
> into an issue, and scrub those fields before any body becomes a fixture.
> `just skill-corpus` also writes the simulation's requests and responses to
> `alexa/dist/dialog/skill-io.json`, which carries the same material — it is
> gitignored, and should be deleted once it has served its purpose.

## Useful lower-level commands

`ask smapi` wraps individual API operations, run from `alexa/`:

```
ask smapi get-skill-status --skill-id <id>
ask smapi get-interaction-model --skill-id <id> --stage development --locale en-US
ask smapi set-interaction-model --skill-id <id> --stage development --locale en-US \
    --interaction-model file:skill-package/interactionModels/custom/en-US.json
```

`set-interaction-model` pushes the model alone, without touching the manifest —
occasionally handy, but `just skill-deploy` is the normal path.

## If you would rather use the console

The console remains available and shows the same skill: **Build → Interaction
Model → JSON Editor** takes the contents of
`alexa/skill-package/interactionModels/custom/en-US.json` directly, and
**Build → Endpoint** takes the HTTPS URL with the trusted-certificate option.
Changes made there are overwritten by the next `just skill-deploy`.
