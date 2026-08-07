package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

// G6 — behavioral sanitization regression on the STREAMING render paths (CO-5). A
// hostile streamed artifact (embedding ANSI/ESC, CR, and BEL) must be escaped in text
// mode so no control byte reaches the terminal, and single-escaped (not double) in
// NDJSON mode so the record round-trips to the original bytes. audit-5-1 exploited
// this clean; this locks it so RenderStreamEvent stays safe.
func TestRenderStreamEvent_SanitizesHostileContent(t *testing.T) {
	const esc = "\x1b"           // the raw ESC byte a hostile agent embeds
	const escJSON = "\\u001b"    // how encoding/json single-escapes it
	const escSanitized = "\\x1b" // how the terminal sanitizer would render it (must NOT reach NDJSON)
	hostile := "line\rCR" + esc + "[31mANSI\x07BEL"
	ev := envelope.StreamEvent{
		Type:     envelope.StreamTypeArtifact,
		Artifact: &envelope.Artifact{Name: "evil", Parts: []envelope.Part{{Text: hostile}}},
	}

	t.Run("text mode escapes control bytes", func(t *testing.T) {
		var buf bytes.Buffer
		r := New(ModeText, &buf, &buf)
		if err := r.RenderStreamEvent(ev); err != nil {
			t.Fatalf("RenderStreamEvent: %v", err)
		}
		out := buf.String()
		if strings.ContainsAny(out, "\x1b\r\x07") {
			t.Errorf("raw control bytes (ESC/CR/BEL) reached the terminal: %q", out)
		}
		if !strings.Contains(out, escSanitized) {
			t.Errorf("ESC should be escaped as %s, got %q", escSanitized, out)
		}
	})

	t.Run("ndjson single-escapes and round-trips", func(t *testing.T) {
		var buf bytes.Buffer
		r := New(ModeJSON, &buf, &buf)
		if err := r.RenderStreamEvent(ev); err != nil {
			t.Fatalf("RenderStreamEvent: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, escJSON) {
			t.Errorf("ESC should be single-escaped by encoding/json as %s, got %q", escJSON, out)
		}
		if strings.Contains(out, escSanitized) {
			t.Errorf("the terminal sanitizer must NOT run over NDJSON (found %s), got %q", escSanitized, out)
		}
		var got envelope.StreamEvent
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("NDJSON record is not valid JSON: %v\n%q", err, out)
		}
		if got.Artifact == nil || len(got.Artifact.Parts) != 1 || got.Artifact.Parts[0].Text != hostile {
			t.Errorf("record did not round-trip to the original bytes (double-escaping would corrupt it): %+v", got.Artifact)
		}
	})
}
