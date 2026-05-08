package config

import (
	"encoding/json"
	"testing"
)

// TestLegacyFocusModeEnabledIsSilentlyIgnored confirms that an existing config
// file containing the removed focus_mode_enabled key still deserializes
// cleanly, leaving other fields intact.
func TestLegacyFocusModeEnabledIsSilentlyIgnored(t *testing.T) {
	data := []byte(`{"focus_mode_enabled": true, "audio_enabled": false, "focus_session_minutes": 60}`)
	var s GlobalSettings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.AudioEnabled == nil || *s.AudioEnabled {
		t.Error("AudioEnabled should be set to false")
	}
	if s.FocusSessionMinutes == nil || *s.FocusSessionMinutes != 60 {
		t.Error("FocusSessionMinutes should be set to 60")
	}
}
