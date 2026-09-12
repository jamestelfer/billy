package alexaverify

import (
	"crypto/x509"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
			require.NoError(t, verifier.Verify(t.Context(), fixture.body, headers))
		})
	}
}

func TestSyntheticCorpusSignaturesRejectReserializedBodies(t *testing.T) {
	fixtures, roots, chain := loadCorpus(t)

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			var envelope any
			require.NoError(t, json.Unmarshal(fixture.body, &envelope), "decoding fixture")
			reserialized, err := json.Marshal(envelope)
			require.NoError(t, err, "re-encoding fixture: %v", err)
			require.NotEqual(t, fixture.body, reserialized,
				"fixture bytes survive JSON reserialization; byte-fidelity tripwire is ineffective")

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
	require.NoError(t, err, "reading corpus root: %v", err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(rootPEM), "corpus root.pem contains no certificate")
	chain, err := os.ReadFile(filepath.Join(rootDir, "chain.pem"))
	require.NoError(t, err, "reading corpus chain: %v", err)

	entries, err := os.ReadDir(rootDir)
	require.NoError(t, err, "reading corpus directory: %v", err)
	fixtures := make([]corpusFixture, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(rootDir, entry.Name())
		body, err := os.ReadFile(filepath.Join(dir, "body.json"))
		require.NoError(t, err, "reading %s body: %v", entry.Name(), err)
		encodedHeaders, err := os.ReadFile(filepath.Join(dir, "headers.json"))
		require.NoError(t, err, "reading %s headers: %v", entry.Name(), err)
		var headers corpusHeaders
		require.NoError(t, json.Unmarshal(encodedHeaders, &headers), "decoding %s headers", entry.Name())
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
	require.Equal(t, want, got, "corpus directories = %v, want %v", got, want)
	return fixtures, roots, chain
}
