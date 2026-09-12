package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBookRejectsInvalidDescriptorsWithFieldDiagnostics(t *testing.T) {
	tests := map[string]struct {
		descriptor string
		want       string
	}{
		"malformed JSON":    {`{"title":`, "decoding book descriptor"},
		"unknown field":     {`{"title":"Story","mp3":"book.mp3","extra":true}`, "unknown field"},
		"blank title":       {`{"title":"  ","mp3":"book.mp3"}`, "field title"},
		"unmatchable title": {`{"title":"!!!","mp3":"book.mp3"}`, "field title"},
		"blank media":       {`{"title":"Story","mp3":"  "}`, "field mp3"},
		"null author":       {`{"title":"Story","author":null,"mp3":"book.mp3"}`, "field author"},
		"trailing value":    {`{"title":"Story","mp3":"book.mp3"} {}`, "exactly one JSON value"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			descriptorName := filepath.Join(t.TempDir(), "book.json")
			require.NoError(t, os.WriteFile(descriptorName, []byte(tc.descriptor), 0o600))

			_, err := loadBook(descriptorName)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLoadBookRejectsAbsoluteAndParentMediaPaths(t *testing.T) {
	for name, mediaName := range map[string]string{
		"absolute":        filepath.Join(t.TempDir(), "outside.mp3"),
		"escaping parent": filepath.Join("..", "outside.mp3"),
		"embedded parent": "audio/../book.mp3",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			descriptor := []byte(`{"title":"Story","mp3":` + fmt.Sprintf("%q", mediaName) + `}`)
			descriptorName := filepath.Join(dir, "book.json")
			require.NoError(t, os.WriteFile(descriptorName, descriptor, 0o600))

			_, err := loadBook(descriptorName)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "field mp3")
		})
	}
}

func TestLoadBookDistinguishesDescriptorAndMediaFailures(t *testing.T) {
	dir := t.TempDir()

	_, descriptorErr := loadBook(filepath.Join(dir, "missing.json"))
	require.Error(t, descriptorErr)
	assert.Contains(t, descriptorErr.Error(), "book descriptor")

	descriptorName := filepath.Join(dir, "book.json")
	require.NoError(t, os.WriteFile(descriptorName, []byte(`{"title":"Story","mp3":"missing.mp3"}`), 0o600))
	_, mediaErr := loadBook(descriptorName)
	require.Error(t, mediaErr)
	assert.Contains(t, mediaErr.Error(), "book media")

	require.NoError(t, os.Mkdir(filepath.Join(dir, "not-an-mp3"), 0o700))
	require.NoError(t, os.WriteFile(descriptorName, []byte(`{"title":"Story","mp3":"not-an-mp3"}`), 0o600))
	_, nonRegularErr := loadBook(descriptorName)
	require.Error(t, nonRegularErr)
	assert.Contains(t, nonRegularErr.Error(), "not a regular file")
}

func TestLoadBookRejectsMediaSymlinkOutsideDescriptorDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "book")
	require.NoError(t, os.Mkdir(dir, 0o700))
	outside := filepath.Join(parent, "outside.mp3")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
	if err := os.Symlink(outside, filepath.Join(dir, "book.mp3")); err != nil {
		t.Skipf("symbolic links are unavailable on this test host: %v", err)
	}
	descriptorName := filepath.Join(dir, "book.json")
	require.NoError(t, os.WriteFile(descriptorName, []byte(`{"title":"Story","mp3":"book.mp3"}`), 0o600))

	_, err := loadBook(descriptorName)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "book media")
}

func TestLoadBookAcceptsAValidExternalDescriptor(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "audio"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "audio", "story.mp3"), []byte("audio bytes"), 0o600))
	descriptorName := filepath.Join(dir, "book.json")
	require.NoError(t, os.WriteFile(descriptorName, []byte(`{
		"title": "A Test Story",
		"author": "A. Writer",
		"mp3": "audio/story.mp3"
	}`), 0o600))

	configuredBook, err := loadBook(descriptorName)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, configuredBook.Close()) })

	assert.Equal(t, "A Test Story", configuredBook.Title)
	assert.Equal(t, "A. Writer", configuredBook.Author)
}
