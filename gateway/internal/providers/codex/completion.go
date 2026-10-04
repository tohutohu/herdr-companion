package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// completionRevision distinguishes new results within a finished turn from
// lifecycle fluctuations. Turn IDs alone would hide background task results;
// timestamps and thread metadata can change without any new agent output.
func completionRevision(turn *Turn) string {
	if turn == nil || turn.ID == "" {
		return ""
	}
	switch turn.Status {
	case "completed", "interrupted", "failed":
	default:
		return ""
	}
	content := struct {
		ID     string
		Status string
		Error  *TurnError
		Items  []json.RawMessage
	}{turn.ID, turn.Status, turn.Error, turn.Items}
	digest := sha256.New()
	// Normalize whitespace and object field order, including raw item JSON.
	// Write directly to the hash instead of materializing another turn copy.
	if err := jsonv2.MarshalWrite(digest, content, jsontext.ReorderRawObjects(true)); err != nil {
		return "" // Unknown revisions retain the lifecycle-only fallback.
	}
	return turn.ID + ":" + hex.EncodeToString(digest.Sum(nil))
}
