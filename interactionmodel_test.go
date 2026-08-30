package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The interaction model shipped in docs/ is pasted straight into the Alexa
// developer console's JSON editor, so a broken model is only discovered by
// hand, in a browser, after a failed build. These tests hold it to the rules
// the console enforces, plus the phase constraints from setup.md: one custom
// intent, the required built-ins, and nothing that turns on AudioPlayer.

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
	if name := language.InvocationName; name != strings.ToLower(name) || strings.TrimSpace(name) == "" {
		t.Errorf("invocationName = %q, want a non-empty lower-case phrase", name)
	}

	for _, intent := range language.Intents {
		builtin := strings.HasPrefix(intent.Name, "AMAZON.")
		for _, sample := range intent.Samples {
			if builtin {
				t.Errorf("intent %s carries sample %q; built-in intents must have no samples", intent.Name, sample)
			}
			if sample != strings.ToLower(sample) {
				t.Errorf("intent %s sample %q is not lower case", intent.Name, sample)
			}
			if strings.ContainsAny(sample, ".,?!;:\"") {
				t.Errorf("intent %s sample %q contains punctuation the console rejects", intent.Name, sample)
			}
			// "alexa ask audiobookshelf to capture this" is spoken as a whole;
			// the sample covers only the part after the invocation name.
			if strings.HasPrefix(sample, language.InvocationName) {
				t.Errorf("intent %s sample %q repeats the invocation name", intent.Name, sample)
			}
		}
	}
}

func TestInteractionModelDeclaresTheRequiredBuiltins(t *testing.T) {
	names := intentNames(t)

	for _, required := range requiredBuiltinIntents {
		if !slices.Contains(names, required) {
			t.Errorf("interaction model is missing %s; got %v", required, names)
		}
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
		if len(intent.Samples) < 2 {
			t.Errorf("custom intent %s has %d samples, want at least 2", intent.Name, len(intent.Samples))
		}
		// A slot means slot resolution can fail before the request is even
		// dispatched, which is a way to lose a capture for no benefit.
		if len(intent.Slots) != 0 {
			t.Errorf("custom intent %s declares %d slots, want none in this phase", intent.Name, len(intent.Slots))
		}
	}

	if custom != 1 {
		t.Errorf("model declares %d custom intents, want exactly 1", custom)
	}
}

func TestInteractionModelDoesNotAnticipateAudioPlayer(t *testing.T) {
	names := intentNames(t)

	for _, intent := range audioPlayerIntents {
		if slices.Contains(names, intent) {
			t.Errorf("interaction model declares %s; AudioPlayer is a later phase", intent)
		}
	}
}

func loadInteractionModel(t *testing.T) interactionModel {
	t.Helper()

	path := filepath.Join("docs", "interaction-model.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var model interactionModel
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
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
