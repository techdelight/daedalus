// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestValidatePersonaName_Valid(t *testing.T) {
	names := []string{"reviewer", "code-review", "my.persona", "persona_v2"}
	for _, name := range names {
		if err := ValidatePersonaName(name); err != nil {
			t.Errorf("ValidatePersonaName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidatePersonaName_Empty(t *testing.T) {
	if err := ValidatePersonaName(""); err == nil {
		t.Error("ValidatePersonaName(\"\") = nil, want error")
	}
}

func TestValidatePersonaName_InvalidChars(t *testing.T) {
	names := []string{"-bad", ".hidden", "my@persona", "a/b"}
	for _, name := range names {
		if err := ValidatePersonaName(name); err == nil {
			t.Errorf("ValidatePersonaName(%q) = nil, want error", name)
		}
	}
}

func TestValidatePersonaName_RejectsBuiltinClaude(t *testing.T) {
	err := ValidatePersonaName("claude")
	if err == nil {
		t.Fatal("ValidatePersonaName(\"claude\") = nil, want error")
	}
	if got := err.Error(); got != `persona name "claude" conflicts with built-in runner` {
		t.Errorf("error = %q, want built-in conflict message", got)
	}
}

func TestValidatePersonaName_RejectsBuiltinCopilot(t *testing.T) {
	err := ValidatePersonaName("copilot")
	if err == nil {
		t.Fatal("ValidatePersonaName(\"copilot\") = nil, want error")
	}
}

func TestIsBuiltinRunner(t *testing.T) {
	if !IsBuiltinRunner("claude") {
		t.Error("IsBuiltinRunner(\"claude\") = false, want true")
	}
	if !IsBuiltinRunner("copilot") {
		t.Error("IsBuiltinRunner(\"copilot\") = false, want true")
	}
	if IsBuiltinRunner("reviewer") {
		t.Error("IsBuiltinRunner(\"reviewer\") = true, want false")
	}
}

func TestPersonasDir(t *testing.T) {
	cfg := &Config{DataDir: "/data/daedalus"}
	got := cfg.PersonasDir()
	want := "/data/daedalus/personas"
	if got != want {
		t.Errorf("PersonasDir() = %q, want %q", got, want)
	}
}

func TestBuiltinRunnerNames(t *testing.T) {
	names := BuiltinRunnerNames()
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["claude"] || !found["copilot"] {
		t.Errorf("BuiltinRunnerNames() = %v, want [claude copilot]", names)
	}
}
