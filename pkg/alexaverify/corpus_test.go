package alexaverify

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

//go:generate go run ./internal/corpusgen

var corpusNow = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

type corpusHeaders struct {
	Signature256        string `json:"signature_256"`
	CertificateChainURL string `json:"certificate_chain_url"`
}

type corpusFixture struct {
	name    string
	body    []byte
	headers corpusHeaders
}

func TestSyntheticSignedRequestCorpus(t *testing.T) {
	fixtures, roots, chain := loadCorpus(t)

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			verifier, _ := verifierServingBundle(t, roots, corpusNow, chain)
			headers := headersForURL(fixture.headers.Signature256, fixture.headers.CertificateChainURL)
			if err := verifier.Verify(t.Context(), fixture.body, headers); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestSyntheticCorpusSignaturesRejectReserializedBodies(t *testing.T) {
	fixtures, roots, chain := loadCorpus(t)

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			var envelope any
			if err := json.Unmarshal(fixture.body, &envelope); err != nil {
				t.Fatalf("decoding fixture: %v", err)
			}
			reserialized, err := json.Marshal(envelope)
			if err != nil {
				t.Fatalf("re-encoding fixture: %v", err)
			}
			if bytes.Equal(reserialized, fixture.body) {
				t.Fatal("fixture bytes survive JSON reserialization; byte-fidelity tripwire is ineffective")
			}

			verifier, _ := verifierServingBundle(t, roots, corpusNow, chain)
			headers := headersForURL(fixture.headers.Signature256, fixture.headers.CertificateChainURL)
			assertOnlySentinel(t, verifier.Verify(t.Context(), reserialized, headers), ErrBadSignature)
		})
	}
}

func loadCorpus(t *testing.T) ([]corpusFixture, *x509.CertPool, []byte) {
	t.Helper()

	rootDir := filepath.Join("testdata", "corpus")
	rootPEM, err := os.ReadFile(filepath.Join(rootDir, "root.pem"))
	if err != nil {
		t.Fatalf("reading corpus root: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("corpus root.pem contains no certificate")
	}
	chain, err := os.ReadFile(filepath.Join(rootDir, "chain.pem"))
	if err != nil {
		t.Fatalf("reading corpus chain: %v", err)
	}

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		t.Fatalf("reading corpus directory: %v", err)
	}
	fixtures := make([]corpusFixture, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(rootDir, entry.Name())
		body, err := os.ReadFile(filepath.Join(dir, "body.json"))
		if err != nil {
			t.Fatalf("reading %s body: %v", entry.Name(), err)
		}
		encodedHeaders, err := os.ReadFile(filepath.Join(dir, "headers.json"))
		if err != nil {
			t.Fatalf("reading %s headers: %v", entry.Name(), err)
		}
		var headers corpusHeaders
		if err := json.Unmarshal(encodedHeaders, &headers); err != nil {
			t.Fatalf("decoding %s headers: %v", entry.Name(), err)
		}
		fixtures = append(fixtures, corpusFixture{name: entry.Name(), body: body, headers: headers})
	}

	slices.SortFunc(fixtures, func(a, b corpusFixture) int {
		if a.name < b.name {
			return -1
		}
		if a.name > b.name {
			return 1
		}
		return 0
	})
	want := []string{
		"AudioPlayer.PlaybackFailed",
		"AudioPlayer.PlaybackFinished",
		"AudioPlayer.PlaybackNearlyFinished",
		"AudioPlayer.PlaybackStarted",
		"AudioPlayer.PlaybackStopped",
		"IntentRequest",
		"LaunchRequest",
	}
	got := make([]string, len(fixtures))
	for index, fixture := range fixtures {
		got[index] = fixture.name
	}
	if !slices.Equal(got, want) {
		t.Fatalf("corpus directories = %v, want %v", got, want)
	}
	return fixtures, roots, chain
}
