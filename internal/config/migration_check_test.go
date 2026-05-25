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

func TestLegacyMaxConcurrentAgentsIsSilentlyIgnored(t *testing.T) {
	data := []byte(`{"max_concurrent_agents": 5, "audio_enabled": true}`)
	var s GlobalSettings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.MaxConcurrentSessions != nil {
		t.Error("MaxConcurrentSessions should be nil when only legacy key is present")
	}
	if s.AudioEnabled == nil || !*s.AudioEnabled {
		t.Error("AudioEnabled should be set to true")
	}
}
