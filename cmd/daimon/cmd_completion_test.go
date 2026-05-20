package main

import (
	"strings"
	"testing"
)

// Smoke test for cmdCompletion. The completion scripts themselves
// are static strings — what we want to assert is (a) the dispatch
// table is correct (each of bash/zsh/fish routes to its script),
// (b) unknown shells error cleanly, (c) every script contains the
// minimum set of top-level subcommands so a careless edit doesn't
// drop one without the test noticing.
//
// Shell-level syntax validation (bash -n, zsh -n) lives in the
// install-script CI shard ideally, but for now is verified by the
// developer running `daimon completion bash | bash -n -` locally.

func TestCompletion_DispatchesPerShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			// cmdCompletion writes to os.Stdout — for unit-test
			// purposes we just exercise the dispatch path (each
			// shell must NOT return an error). Stdout capture is
			// possible but adds complexity for low marginal value
			// against the existing static-string assertions.
			if err := cmdCompletion([]string{shell}); err != nil {
				t.Errorf("cmdCompletion(%q) returned error: %v", shell, err)
			}
		})
	}
}

func TestCompletion_RejectsUnknownShell(t *testing.T) {
	if err := cmdCompletion([]string{"powershell"}); err == nil {
		t.Error("cmdCompletion(powershell): expected error, got nil")
	}
}

func TestCompletion_RejectsWrongArgCount(t *testing.T) {
	if err := cmdCompletion(nil); err == nil {
		t.Error("cmdCompletion(nil): expected error, got nil")
	}
	if err := cmdCompletion([]string{"bash", "extra"}); err == nil {
		t.Error("cmdCompletion(bash, extra): expected error, got nil")
	}
}

// Anti-regression for the "I dropped a subcommand from completion
// without noticing" case. The bash + zsh + fish scripts each
// enumerate the top-level verbs explicitly; if a new verb lands in
// main.go's switch but doesn't land in the completion script, the
// completion silently misses it. This test pins the minimum set so
// any drop-out trips a unit-test failure.
//
// Adding a new top-level verb? Update completion + this test
// together. The CONTRIBUTING.md PR template line points at this.
func TestCompletion_AllShellsContainCoreVerbs(t *testing.T) {
	core := []string{
		"init", "unlock", "identity", "memory", "provider",
		"chat", "doctor", "activity", "wallet", "payment",
		"rotate-password", "backup", "restore", "completion",
	}
	for _, shell := range []struct {
		name   string
		script string
	}{
		{"bash", completionBash},
		{"zsh", completionZsh},
		{"fish", completionFish},
	} {
		t.Run(shell.name, func(t *testing.T) {
			for _, verb := range core {
				if !strings.Contains(shell.script, verb) {
					t.Errorf("%s completion script missing top-level verb %q", shell.name, verb)
				}
			}
		})
	}
}

func TestCompletion_AllShellsContainMemoryKinds(t *testing.T) {
	// Same anti-regression for the 'fact|preference|task|observation'
	// canonical kind list. If the kinds change at the
	// internal/memory level, completion has to follow.
	kinds := []string{"fact", "preference", "task", "observation"}
	for _, shell := range []struct {
		name   string
		script string
	}{
		{"bash", completionBash},
		{"zsh", completionZsh},
		{"fish", completionFish},
	} {
		t.Run(shell.name, func(t *testing.T) {
			for _, k := range kinds {
				if !strings.Contains(shell.script, k) {
					t.Errorf("%s completion script missing memory kind %q", shell.name, k)
				}
			}
		})
	}
}
