# Run all checks before committing
verify: fmt build lint test

# Format all Go source files
fmt:
    gofmt -w .

# Run all tests
test *args:
    go test ./... {{args}}

# Build the binary
build *args:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p dist
    env CGO_ENABLED=0 go build -trimpath -o dist/ . {{args}}

# Run linter
lint:
    golangci-lint run ./...

# Cross-platform snapshot build for every release matrix target (via goreleaser)
xbuild *args:
    goreleaser build --snapshot --clean {{args}}

# --- Alexa skill (ASK CLI) -------------------------------------------------
# The skill lives in alexa/ as an ASK CLI project, and ask-cli expects to run
# from that directory: ask-resources.json, .ask/ and the skill package are all
# resolved relative to it. skill.json is a template — the endpoint URI is the
# tailnet's Funnel URL, which is per-deployment, so it is substituted from the
# environment into alexa/dist/ (gitignored) at deploy time rather than
# committed.

# Render skill-package with the live endpoint substituted in
[working-directory('alexa')]
skill-render:
    #!/usr/bin/env bash
    set -euo pipefail
    : "${BILLY_SKILL_ENDPOINT:?set it to the Funnel endpoint, e.g. https://billy.<tailnet>.ts.net/alexa}"
    rm -rf dist/skill-package
    mkdir -p dist
    cp -R skill-package dist/skill-package
    sed "s|@BILLY_SKILL_ENDPOINT@|${BILLY_SKILL_ENDPOINT}|g" \
        skill-package/skill.json > dist/skill-package/skill.json
    if grep -q '@BILLY_SKILL_ENDPOINT@' dist/skill-package/skill.json; then
        echo "endpoint substitution failed" >&2
        exit 1
    fi

# Create or update the skill in the developer account (first run creates it)
[working-directory('alexa')]
skill-deploy *args: skill-render
    ask deploy {{args}}

# Talk to the deployed skill interactively (.quit to exit, .record to save)
[working-directory('alexa')]
skill-talk *args:
    ask dialog --locale en-US --stage development {{args}}

# Replay the scripted utterances in alexa/dialog/corpus.json to fill the corpus
[working-directory('alexa')]
skill-corpus:
    #!/usr/bin/env bash
    set -euo pipefail
    states=.ask/ask-states.json
    [[ -f $states ]] || { echo "no alexa/$states — run 'just skill-deploy' first" >&2; exit 1; }
    skill_id=$(jq -r '.profiles.default.skillId // empty' "$states")
    [[ -n $skill_id ]] || { echo "no skillId in alexa/$states — run 'just skill-deploy' first" >&2; exit 1; }
    mkdir -p dist/dialog
    # The replay file must carry the skill id itself; ask dialog ignores
    # --skill-id in replay mode.
    jq --arg id "$skill_id" '. + {skillId: $id, locale: "en-US"}' \
        dialog/corpus.json > dist/dialog/corpus.json
    # --save-skill-io records the requests Alexa sent, which carry a live
    # apiAccessToken. It stays under the gitignored alexa/dist/.
    ask dialog --replay dist/dialog/corpus.json --save-skill-io dist/dialog/skill-io.json
