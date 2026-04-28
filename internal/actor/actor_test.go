package actor_test

import (
	"strings"
	"testing"

	"github.com/frane/agented/internal/actor"
)

func TestFlagWins(t *testing.T) {
	t.Setenv("AE_ACTOR", "envactor")
	got, err := actor.Resolve("flagactor", "cfgactor")
	if err != nil {
		t.Fatal(err)
	}
	if got != "flagactor" {
		t.Errorf("got %q", got)
	}
}

func TestEnvBeatsConfig(t *testing.T) {
	t.Setenv("AE_ACTOR", "envactor")
	got, err := actor.Resolve("", "cfgactor")
	if err != nil {
		t.Fatal(err)
	}
	if got != "envactor" {
		t.Errorf("got %q", got)
	}
}

func TestConfigBeatsFallback(t *testing.T) {
	t.Setenv("AE_ACTOR", "")
	got, err := actor.Resolve("", "cfgactor")
	if err != nil {
		t.Fatal(err)
	}
	if got != "cfgactor" {
		t.Errorf("got %q", got)
	}
}

func TestFallback(t *testing.T) {
	t.Setenv("AE_ACTOR", "")
	got, err := actor.Resolve("", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, ":") {
		t.Errorf("fallback should contain ':' separator, got %q", got)
	}
}
