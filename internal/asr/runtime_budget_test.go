package asr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeBudgetDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"enabled":false,"runtime":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Runtime.Threads; got != 2 {
		t.Fatalf("default runtime threads = %d，want 2", got)
	}
	if DefaultRuntimeThreads != 2 {
		t.Fatalf("DefaultRuntimeThreads = %d，want 2", DefaultRuntimeThreads)
	}
	if got := cfg.Runtime.IdleTimeoutSeconds; got != 300 {
		t.Fatalf("default idle timeout = %d，want 300", got)
	}
	if DefaultIdleTimeoutSeconds != 300 {
		t.Fatalf("DefaultIdleTimeoutSeconds = %d，want 300", DefaultIdleTimeoutSeconds)
	}
	if got := cfg.Runtime.Provider; got != "cpu" {
		t.Fatalf("default provider = %q，want cpu", got)
	}
}

func TestRuntimeBudgetExplicitValuesArePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"enabled": false,
		"runtime": {"threads": 6, "provider": "coreml", "idleTimeoutSeconds": 42}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.Threads != 6 || cfg.Runtime.Provider != "coreml" || cfg.Runtime.IdleTimeoutSeconds != 42 {
		t.Fatalf("explicit runtime budget was changed: %+v", cfg.Runtime)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid explicit runtime budget rejected: %v", err)
	}
}

func TestRuntimeBudgetValidationRejectsOutOfBoundsValues(t *testing.T) {
	tests := []struct {
		name    string
		runtime RuntimeSpec
		want    string
	}{
		{name: "negative threads", runtime: RuntimeSpec{Threads: -1}, want: "runtime.threads"},
		{name: "too many threads", runtime: RuntimeSpec{Threads: MaxRuntimeThreads + 1}, want: "runtime.threads"},
		{name: "negative idle", runtime: RuntimeSpec{Threads: 2, IdleTimeoutSeconds: -1}, want: "runtime.idleTimeoutSeconds"},
		{name: "too long idle", runtime: RuntimeSpec{Threads: 2, IdleTimeoutSeconds: MaxIdleTimeoutSeconds + 1}, want: "runtime.idleTimeoutSeconds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Runtime: test.runtime}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v，want field %s", err, test.want)
			}
		})
	}
}

func TestRuntimeBudgetValidationAcceptsBoundsAndMissingDefaults(t *testing.T) {
	for _, runtime := range []RuntimeSpec{
		{},
		{Threads: MinRuntimeThreads, IdleTimeoutSeconds: MinIdleTimeoutSeconds},
		{Threads: MaxRuntimeThreads, IdleTimeoutSeconds: MaxIdleTimeoutSeconds},
	} {
		cfg := &Config{Runtime: runtime}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate(%+v) failed: %v", runtime, err)
		}
	}
}
