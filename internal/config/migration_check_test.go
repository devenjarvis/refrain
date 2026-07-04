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
}

func TestLegacyMaxConcurrentAgentsIsSilentlyIgnored(t *testing.T) {
	data := []byte(`{"max_concurrent_agents": 5, "audio_enabled": true}`)
	var s GlobalSettings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.AudioEnabled == nil || !*s.AudioEnabled {
		t.Error("AudioEnabled should be set to true")
	}
}

// TestLegacyWellnessKeysAreSilentlyIgnored covers the five keys removed in the
// rollback's Phase 5 (wellness timers, session caps, review backlog, and
// plan-first gating): a config file carrying all of them must still load, and
// live keys in the same file must survive.
func TestLegacyWellnessKeysAreSilentlyIgnored(t *testing.T) {
	data := []byte(`{
		"focus_session_minutes": 90,
		"focus_break_minutes": 15,
		"max_concurrent_sessions": 3,
		"max_review_backlog": 5,
		"plan_first_enabled": true,
		"branch_prefix": "me/"
	}`)
	var s GlobalSettings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.BranchPrefix == nil || *s.BranchPrefix != "me/" {
		t.Error("BranchPrefix should survive alongside legacy wellness keys")
	}
}
