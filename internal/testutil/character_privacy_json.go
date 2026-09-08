package testutil

import (
	"encoding/json"
	"testing"
	"time"
)

// CharacterObservationPrivacyJSON excludes only the observation envelope's
// Host wall-clock timestamp, whose seconds can resemble a forbidden amount.
// The source bytes and digest remain unchanged; all other fields are retained.
func CharacterObservationPrivacyJSON(t testing.TB, raw json.RawMessage) json.RawMessage {
	t.Helper()
	return characterPrivacyJSONWithoutHostTimestamp(t, raw, "generated_at")
}

// CharacterMemoryPrivacyJSON excludes only the memory envelope's Host update
// timestamp. In particular, timestamps or same-named fields inside facts stay.
func CharacterMemoryPrivacyJSON(t testing.TB, raw json.RawMessage) json.RawMessage {
	t.Helper()
	return characterPrivacyJSONWithoutHostTimestamp(t, raw, "updated_at")
}

func characterPrivacyJSONWithoutHostTimestamp(t testing.TB, raw json.RawMessage, field string) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var stamp string
	if value, ok := fields[field]; ok && json.Unmarshal(value, &stamp) == nil {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			delete(fields, field)
		}
	}
	facts, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}
