package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alexa/ is an ASK CLI project: `just skill-deploy` renders it and hands the
// skill package to Amazon, which is the only thing that validates it. A
// mistake there surfaces as a failed import minutes later, so these tests
// hold the package to the rules the console and certification enforce, plus
// the phase constraints from setup.md: one custom intent, the required
// built-ins, and nothing that turns on AudioPlayer.
//
// They deliberately read the *tracked* package, not the rendered copy under
// alexa/dist/ — the endpoint placeholder is part of what is being checked.

// requiredBuiltinIntents are mandatory in every custom skill's model.
// AMAZON.FallbackIntent is not mandatory, but it is what an unrecognised
// utterance lands on, which makes it a cheap extra envelope shape to capture.
var requiredBuiltinIntents = []string{
	"AMAZON.CancelIntent",
	"AMAZON.HelpIntent",
	"AMAZON.StopIntent",
	"AMAZON.NavigateHomeIntent",
	"AMAZON.FallbackIntent",
}

// audioPlayerIntents are required *only* once the AudioPlayer interface is
// enabled, which this phase deliberately does not do (setup.md, Phase 3).
// Their presence would mean the model had drifted ahead of the endpoint.
var audioPlayerIntents = []string{
	"AMAZON.PauseIntent",
	"AMAZON.ResumeIntent",
	"AMAZON.LoopOffIntent",
	"AMAZON.LoopOnIntent",
	"AMAZON.NextIntent",
	"AMAZON.PreviousIntent",
	"AMAZON.RepeatIntent",
	"AMAZON.ShuffleOffIntent",
	"AMAZON.ShuffleOnIntent",
	"AMAZON.StartOverIntent",
}

type interactionModel struct {
	InteractionModel struct {
		LanguageModel struct {
			InvocationName string `json:"invocationName"`
			Intents        []struct {
				Name    string   `json:"name"`
				Samples []string `json:"samples"`
				Slots   []struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"slots"`
			} `json:"intents"`
		} `json:"languageModel"`
	} `json:"interactionModel"`
}

func TestInteractionModelIsValidForTheConsole(t *testing.T) {
	model := loadInteractionModel(t)
	language := model.InteractionModel.LanguageModel

	// The console rejects an invocation name that is not lower case, and
	// speech recognition never produces anything else.
	name := language.InvocationName
	assert.NotEmpty(t, strings.TrimSpace(name), "invocationName must not be empty")
	assert.Equal(t, strings.ToLower(name), name, "invocationName must be lower case")

	for _, intent := range language.Intents {
		if strings.HasPrefix(intent.Name, "AMAZON.") {
			assert.Empty(t, intent.Samples, "built-in intent %s must have no samples", intent.Name)
			continue
		}
		for _, sample := range intent.Samples {
			assert.Equal(t, strings.ToLower(sample), sample, "intent %s sample %q is not lower case", intent.Name, sample)
			assert.NotRegexp(t, `[.,?!;:"]`, sample, "intent %s sample %q contains punctuation the console rejects", intent.Name, sample)
			// "alexa ask audiobookshelf to capture this" is spoken as a whole;
			// the sample covers only the part after the invocation name.
			assert.NotRegexp(t, "^"+regexp.QuoteMeta(language.InvocationName), sample,
				"intent %s sample %q repeats the invocation name", intent.Name, sample)
		}
	}
}

func TestInteractionModelDeclaresTheRequiredBuiltins(t *testing.T) {
	names := intentNames(t)

	for _, required := range requiredBuiltinIntents {
		assert.Contains(t, names, required, "interaction model is missing %s; got %v", required, names)
	}
}

// setup.md Phase 3: one custom intent with a couple of sample utterances is
// enough to produce an IntentRequest, which is the second envelope shape the
// corpus needs alongside the LaunchRequest.
func TestInteractionModelHasOneCustomIntentWithSamples(t *testing.T) {
	model := loadInteractionModel(t)

	custom := 0
	for _, intent := range model.InteractionModel.LanguageModel.Intents {
		if strings.HasPrefix(intent.Name, "AMAZON.") {
			continue
		}
		custom++
		assert.GreaterOrEqual(t, len(intent.Samples), 2, "custom intent %s has %d samples, want at least 2", intent.Name, len(intent.Samples))
		// A slot means slot resolution can fail before the request is even
		// dispatched, which is a way to lose a capture for no benefit.
		assert.Empty(t, intent.Slots, "custom intent %s declares %d slots, want none in this phase", intent.Name, len(intent.Slots))
	}

	assert.Equal(t, 1, custom, "model declares %d custom intents, want exactly 1", custom)
}

func TestInteractionModelDoesNotAnticipateAudioPlayer(t *testing.T) {
	names := intentNames(t)

	for _, intent := range audioPlayerIntents {
		assert.NotContains(t, names, intent, "interaction model declares %s; AudioPlayer is a later phase", intent)
	}
}

// skillPackageDir is the tracked skill package. just skill-render copies it
// to alexa/dist/skill-package with the endpoint substituted in; that copy is
// what ask deploy uploads.
var skillPackageDir = filepath.Join("alexa", "skill-package")

// endpointPlaceholder stands in for the Funnel URL in the tracked manifest.
// The real URL carries the tailnet name and varies per deployment, so it is
// substituted from BILLY_SKILL_ENDPOINT at render time.
const endpointPlaceholder = "@BILLY_SKILL_ENDPOINT@"

func loadInteractionModel(t *testing.T) interactionModel {
	t.Helper()

	var model interactionModel
	readSkillFile(t, filepath.Join(skillPackageDir, "interactionModels", "custom", "en-US.json"), &model)
	return model
}

func intentNames(t *testing.T) []string {
	t.Helper()

	model := loadInteractionModel(t)
	names := make([]string, 0, len(model.InteractionModel.LanguageModel.Intents))
	for _, intent := range model.InteractionModel.LanguageModel.Intents {
		names = append(names, intent.Name)
	}
	return names
}

// skillManifest is the subset of skill.json these tests reason about.
type skillManifest struct {
	Manifest struct {
		PublishingInformation struct {
			Locales map[string]struct {
				Name           string   `json:"name"`
				Summary        string   `json:"summary"`
				Description    string   `json:"description"`
				ExamplePhrases []string `json:"examplePhrases"`
			} `json:"locales"`
		} `json:"publishingInformation"`
		Apis struct {
			Custom struct {
				Endpoint struct {
					URI                string `json:"uri"`
					SSLCertificateType string `json:"sslCertificateType"`
				} `json:"endpoint"`
			} `json:"custom"`
			AudioPlayer *struct{} `json:"audioPlayer"`
		} `json:"apis"`
	} `json:"manifest"`
}

// dialogReplay is alexa/dialog/corpus.json: the scripted utterances
// `just skill-corpus` replays to fill the capture directory. ask dialog wants
// skillId and locale in the file too; those are per-account, so the render
// step adds them from .ask/ask-states.json.
type dialogReplay struct {
	Type      string   `json:"type"`
	UserInput []string `json:"userInput"`
}

// R12/R13: the tracked manifest must never carry a real endpoint. The Funnel
// URL contains the tailnet name and changes per deployment, and the whole
// point of the render step is that it stays out of git.
func TestSkillManifestKeepsTheEndpointOutOfGit(t *testing.T) {
	manifest := loadSkillManifest(t)
	endpoint := manifest.Manifest.Apis.Custom.Endpoint

	assert.Equal(t, endpointPlaceholder, endpoint.URI, "manifest endpoint uri = %q, want the placeholder %q — the real URL is substituted by `just skill-render`", endpoint.URI, endpointPlaceholder)
	// Tailscale provisions a genuine publicly-trusted certificate for the
	// ts.net name, so neither SelfSigned nor Wildcard applies.
	assert.Equal(t, "Trusted", endpoint.SSLCertificateType, "manifest sslCertificateType = %q, want %q", endpoint.SSLCertificateType, "Trusted")
	assert.Nil(t, manifest.Manifest.Apis.AudioPlayer, "manifest enables the AudioPlayer interface; that is a later phase")
}

// Certification requires every example phrase to be a real invocation drawn
// from the model's sample utterances. Nothing checks that until submission,
// and a phrase that does not work is misleading long before then.
func TestSkillManifestExamplePhrasesMatchTheInteractionModel(t *testing.T) {
	model := loadInteractionModel(t).InteractionModel.LanguageModel
	locale := loadSkillManifest(t).Manifest.PublishingInformation.Locales["en-US"]

	assert.Len(t, locale.ExamplePhrases, 3, "en-US examplePhrases has %d entries, want exactly 3", len(locale.ExamplePhrases))
	for _, field := range []struct{ name, value string }{
		{"name", locale.Name},
		{"summary", locale.Summary},
		{"description", locale.Description},
	} {
		assert.NotEmpty(t, strings.TrimSpace(field.value), "en-US %s is empty; the skill package import rejects that", field.name)
	}

	for _, phrase := range locale.ExamplePhrases {
		if !assert.Regexp(t, `^Alexa, `, phrase,
			"example phrase %q does not start with the wake word", phrase) {
			continue
		}
		utterance := strings.TrimPrefix(phrase, "Alexa, ")
		assert.True(t, utteranceReachesSkill(utterance, model.InvocationName, customSamples(t)), "example phrase %q neither launches the skill nor matches a sample utterance", phrase)
	}
}

// A replayed utterance that does not reach the skill produces no request and
// so no capture, which is the entire point of running the replay.
func TestDialogCorpusReachesTheSkill(t *testing.T) {
	var replay dialogReplay
	readSkillFile(t, filepath.Join("alexa", "dialog", "corpus.json"), &replay)

	assert.Equal(t, "text", replay.Type, "corpus type = %q, want %q", replay.Type, "text")
	require.NotEmpty(t, replay.UserInput, "corpus has no userInput; there is nothing to replay")

	model := loadInteractionModel(t).InteractionModel.LanguageModel
	samples := customSamples(t)

	var launches, intents int
	for _, utterance := range replay.UserInput {
		// ask dialog takes what the user says after the wake word.
		assert.NotRegexp(t, `^alexa`, strings.ToLower(utterance), "corpus utterance %q includes the wake word; ask dialog does not want it", utterance)
		assert.Contains(t, utterance, model.InvocationName, "corpus utterance %q never names the skill, so Alexa will not route it here", utterance)
		if utteranceReachesSkill(utterance, model.InvocationName, samples) {
			if strings.HasSuffix(utterance, model.InvocationName) {
				launches++
			} else {
				intents++
			}
		}
	}

	// setup.md Phase 3 wants at least two distinct envelope shapes.
	assert.NotEqual(t, 0, launches, "corpus never just opens the skill, so it captures no LaunchRequest")
	assert.NotEqual(t, 0, intents, "corpus never matches a sample utterance, so it captures no IntentRequest")
}

// utteranceReachesSkill reports whether Alexa would route the utterance to
// this skill: either it opens the skill by name, or it is one of the custom
// intent's samples spoken after the invocation name.
func utteranceReachesSkill(utterance, invocationName string, samples []string) bool {
	utterance = strings.ToLower(strings.TrimSpace(utterance))

	for _, opener := range []string{"open ", "launch ", "start ", "talk to ", "ask "} {
		if !strings.HasPrefix(utterance, opener) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(utterance, opener))
		if rest == invocationName {
			return true
		}
		tail, ok := strings.CutPrefix(rest, invocationName+" ")
		if !ok {
			continue
		}
		return slices.Contains(samples, strings.TrimSpace(tail))
	}
	return false
}

// customSamples is every sample utterance across the model's custom intents.
func customSamples(t *testing.T) []string {
	t.Helper()

	var samples []string
	for _, intent := range loadInteractionModel(t).InteractionModel.LanguageModel.Intents {
		if !strings.HasPrefix(intent.Name, "AMAZON.") {
			samples = append(samples, intent.Samples...)
		}
	}
	return samples
}

func loadSkillManifest(t *testing.T) skillManifest {
	t.Helper()

	var manifest skillManifest
	readSkillFile(t, filepath.Join(skillPackageDir, "skill.json"), &manifest)
	return manifest
}

func readSkillFile(t *testing.T, path string, into any) {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s: %v", path, err)
	require.NoError(t, json.Unmarshal(raw, into), "parsing %s", path)
}
