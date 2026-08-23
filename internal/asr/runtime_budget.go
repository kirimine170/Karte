package asr

import "fmt"

const (
	// DefaultRuntimeThreads leaves headroom for the local LLM and UI work while
	// ASR is decoding．Native recognizers still allow an explicit bounded
	// override for machines with a different resource budget．
	DefaultRuntimeThreads = 2
	MinRuntimeThreads     = 1
	MaxRuntimeThreads     = 8

	DefaultIdleTimeoutSeconds = 300
	MinIdleTimeoutSeconds     = 1
	MaxIdleTimeoutSeconds     = 24 * 60 * 60
)

func applyRuntimeDefaults(runtime *RuntimeSpec) {
	if runtime.Threads == 0 {
		runtime.Threads = DefaultRuntimeThreads
	}
	if runtime.Provider == "" {
		runtime.Provider = "cpu"
	}
	if runtime.IdleTimeoutSeconds == 0 {
		runtime.IdleTimeoutSeconds = DefaultIdleTimeoutSeconds
	}
}

func validateRuntimeSpec(runtime RuntimeSpec) error {
	if runtime.Threads != 0 && (runtime.Threads < MinRuntimeThreads || runtime.Threads > MaxRuntimeThreads) {
		return fmt.Errorf("runtime.threads must be between %d and %d", MinRuntimeThreads, MaxRuntimeThreads)
	}
	if runtime.IdleTimeoutSeconds != 0 &&
		(runtime.IdleTimeoutSeconds < MinIdleTimeoutSeconds || runtime.IdleTimeoutSeconds > MaxIdleTimeoutSeconds) {
		return fmt.Errorf(
			"runtime.idleTimeoutSeconds must be between %d and %d",
			MinIdleTimeoutSeconds,
			MaxIdleTimeoutSeconds,
		)
	}
	return nil
}
