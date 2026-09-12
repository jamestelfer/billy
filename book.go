package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// book is the single catalog item validated at startup. root confines every
// later media open to the descriptor directory, including opens that traverse
// symbolic links.
type book struct {
	Title     string
	Author    string
	mediaName string
	root      *os.Root
}

type bookDescriptor struct {
	Title  string          `json:"title"`
	Author json.RawMessage `json:"author,omitempty"`
	MP3    string          `json:"mp3"`
}

// loadBook validates the external descriptor and its media before the public
// listener is opened. The descriptor remains a runtime input: neither it nor
// its media is copied into the executable or build output.
func loadBook(descriptorName string) (*book, error) {
	f, err := os.Open(descriptorName) //nolint:gosec // operator-selected runtime content is this function's contract
	if err != nil {
		return nil, fmt.Errorf("opening book descriptor %q: %w", descriptorName, err)
	}
	defer func() { _ = f.Close() }()

	var descriptor bookDescriptor
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return nil, fmt.Errorf("decoding book descriptor %q: %w", descriptorName, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, fmt.Errorf("decoding book descriptor %q: %w", descriptorName, err)
	}

	if strings.TrimSpace(descriptor.Title) == "" {
		return nil, errors.New("book descriptor field title must be a non-empty string")
	}
	if normalizeTitle(descriptor.Title) == "" {
		return nil, errors.New("book descriptor field title must contain a letter or digit")
	}
	if strings.TrimSpace(descriptor.MP3) == "" {
		return nil, errors.New("book descriptor field mp3 must be a non-empty string")
	}
	var author string
	if descriptor.Author != nil {
		if bytes.Equal(bytes.TrimSpace(descriptor.Author), []byte("null")) {
			return nil, errors.New("book descriptor field author must be a string when present")
		}
		if err := json.Unmarshal(descriptor.Author, &author); err != nil {
			return nil, fmt.Errorf("book descriptor field author must be a string when present: %w", err)
		}
	}
	if filepath.IsAbs(descriptor.MP3) {
		return nil, errors.New("book descriptor field mp3 must be relative to the descriptor directory")
	}
	if containsParentElement(descriptor.MP3) {
		return nil, errors.New("book descriptor field mp3 must not contain parent-directory traversal")
	}
	if !filepath.IsLocal(descriptor.MP3) {
		return nil, errors.New("book descriptor field mp3 must name a local path within the descriptor directory")
	}

	descriptorDir := filepath.Dir(descriptorName)
	root, err := os.OpenRoot(descriptorDir)
	if err != nil {
		return nil, fmt.Errorf("opening book descriptor directory %q: %w", descriptorDir, err)
	}

	loaded := &book{
		Title:     descriptor.Title,
		Author:    author,
		mediaName: descriptor.MP3,
		root:      root,
	}
	if err := loaded.validateMedia(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return loaded, nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("descriptor must contain exactly one JSON value")
	}
	return err
}

func normalizeTitle(title string) string {
	var normalized strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			normalized.WriteRune(unicode.ToLower(r))
		}
	}
	return normalized.String()
}

func containsParentElement(name string) bool {
	elements := strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return slices.Contains(elements, "..")
}

func (b *book) validateMedia() error {
	f, err := b.openMedia()
	if err != nil {
		return err
	}
	return f.Close()
}

func (b *book) openMedia() (*os.File, error) {
	f, err := b.root.Open(b.mediaName)
	if err != nil {
		return nil, fmt.Errorf("opening book media from descriptor field mp3 %q: %w", b.mediaName, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("inspecting book media from descriptor field mp3 %q: %w", b.mediaName, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("book media from descriptor field mp3 %q is not a regular file", b.mediaName)
	}
	return f, nil
}

func (b *book) Close() error {
	return b.root.Close()
}
