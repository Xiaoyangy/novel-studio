package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

const AuthorSourcesPolicyV1 = "author-sources.v1"

type AuthorSourceV1 struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Digest string `json:"digest"`
}

type AuthorSourcesV1 struct {
	Policy  string           `json:"policy"`
	Sources []AuthorSourceV1 `json:"sources"`
	Digest  string           `json:"digest"`
}

type AuthorSourceParagraphRefV1 struct {
	SourceID  string `json:"source_id"`
	Paragraph int    `json:"paragraph"`
}

func (ref *AuthorSourceParagraphRefV1) UnmarshalJSON(raw []byte) error {
	var wire struct {
		SourceID  *string `json:"source_id"`
		Paragraph *int    `json:"paragraph"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return fmt.Errorf("author paragraph reference: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("author paragraph reference contains trailing JSON")
	}
	if wire.SourceID == nil || wire.Paragraph == nil {
		return fmt.Errorf("author paragraph reference requires explicit non-null source_id and paragraph")
	}
	*ref = AuthorSourceParagraphRefV1{SourceID: *wire.SourceID, Paragraph: *wire.Paragraph}
	return nil
}

type CompassAuthorContractsV1 struct {
	Policy        string                       `json:"policy"`
	SourcesDigest string                       `json:"sources_digest"`
	Refs          []AuthorSourceParagraphRefV1 `json:"refs"`
}

func validAuthorSourceIDV1(id string) bool {
	if len(id) == 0 || len(id) > 128 || !utf8.ValidString(id) || strings.TrimSpace(id) != id {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func authorSourceBytesDigestV1(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FinalizeAuthorSourcesV1 preserves every original text byte, including line
// endings and surrounding whitespace. Existing digests are checked, never
// silently repaired after their source text has changed.
func FinalizeAuthorSourcesV1(value AuthorSourcesV1) (AuthorSourcesV1, error) {
	if value.Policy == "" {
		value.Policy = AuthorSourcesPolicyV1
	}
	if value.Policy != AuthorSourcesPolicyV1 || len(value.Sources) > 32 {
		return value, fmt.Errorf("invalid author sources policy or source count (maximum 32)")
	}
	value.Sources = append([]AuthorSourceV1(nil), value.Sources...)
	seen, total := map[string]bool{}, 0
	for i := range value.Sources {
		source := &value.Sources[i]
		if !validAuthorSourceIDV1(source.ID) || seen[source.ID] || !utf8.ValidString(source.Text) || strings.TrimSpace(source.Text) == "" || len(source.Text) > 1<<20 {
			return value, fmt.Errorf("invalid, duplicate, empty, or oversized author source %q", source.ID)
		}
		seen[source.ID] = true
		total += len(source.Text)
		if total > 4<<20 {
			return value, fmt.Errorf("author source catalog exceeds 4 MiB")
		}
		digest := authorSourceBytesDigestV1([]byte(source.Text))
		if source.Digest != "" && source.Digest != digest {
			return value, fmt.Errorf("author source %q text digest mismatch", source.ID)
		}
		source.Digest = digest
	}
	claimed := value.Digest
	value.Digest = ""
	raw, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	value.Digest = authorSourceBytesDigestV1(raw)
	if claimed != "" && claimed != value.Digest {
		return value, fmt.Errorf("author sources catalog digest mismatch")
	}
	return value, nil
}

// AuthorSourceParagraphsV1 splits only at blank lines. It preserves text and
// line endings inside each paragraph and trims only the paragraph's outside
// whitespace. A reference cannot select a sentence fragment or alter a number.
func AuthorSourceParagraphsV1(text string) []string {
	var paragraphs []string
	start, position := 0, 0
	for position < len(text) {
		lineStart := position
		end := strings.IndexAny(text[position:], "\r\n")
		if end < 0 {
			position = len(text)
		} else {
			position += end + 1
			if text[position-1] == '\r' && position < len(text) && text[position] == '\n' {
				position++
			}
		}
		if strings.TrimSpace(text[lineStart:position]) == "" {
			if paragraph := strings.TrimSpace(text[start:lineStart]); paragraph != "" {
				paragraphs = append(paragraphs, paragraph)
			}
			start = position
		}
	}
	if paragraph := strings.TrimSpace(text[start:]); paragraph != "" {
		paragraphs = append(paragraphs, paragraph)
	}
	return paragraphs
}

func MaterializeCompassAuthorContractsV1(compass StoryCompass, catalog AuthorSourcesV1) (StoryCompass, error) {
	verified, err := FinalizeAuthorSourcesV1(catalog)
	if err != nil {
		return compass, err
	}
	if !reflect.DeepEqual(verified, catalog) {
		return compass, fmt.Errorf("compass requires a finalized author source catalog")
	}
	binding := compass.AuthorContracts
	if binding == nil || binding.Policy != catalog.Policy || binding.SourcesDigest != catalog.Digest || len(binding.Refs) > 4096 {
		return compass, fmt.Errorf("compass author contracts lack the exact catalog policy/digest or exceed reference bounds")
	}
	sources := map[string][]string{}
	for _, source := range catalog.Sources {
		sources[source.ID] = AuthorSourceParagraphsV1(source.Text)
	}
	seen := map[AuthorSourceParagraphRefV1]bool{}
	var contracts []string
	for _, ref := range binding.Refs {
		paragraphs, exists := sources[ref.SourceID]
		if !exists || ref.Paragraph < 0 || ref.Paragraph >= len(paragraphs) || seen[ref] {
			return compass, fmt.Errorf("compass author paragraph reference is missing, duplicated, or out of range")
		}
		seen[ref] = true
		contracts = append(contracts, paragraphs[ref.Paragraph])
	}
	if len(compass.NonNegotiables) > 0 && !reflect.DeepEqual(compass.NonNegotiables, contracts) {
		return compass, fmt.Errorf("compass hard contracts must equal the complete referenced author paragraphs")
	}
	compass.NonNegotiables = contracts
	owned := *binding
	owned.Refs = append([]AuthorSourceParagraphRefV1(nil), binding.Refs...)
	compass.AuthorContracts = &owned
	return compass, nil
}

// CompassHardContractsV1 leaves ending direction soft for source-bound
// compasses. Legacy callers retain their original raw ending contract.
func CompassHardContractsV1(compass StoryCompass) []string {
	var contracts []string
	if compass.AuthorContracts == nil && compass.EndingDirection != "" {
		contracts = append(contracts, compass.EndingDirection)
	}
	return append(contracts, compass.NonNegotiables...)
}
