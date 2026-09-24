package contextcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// mcpScope.json is a private, machine-readable marker placed inside a
// Codex-dedicated KARTE_DATA_DIR. It records the exact policy the MCP
// adapter is allowed to use. The adapter refuses to start unless this file
// exists, parses strictly, and names an actor whose policy grants at least
// search and read.
type MCPScope struct {
	ProtocolVersion string   `json:"protocol_version"`
	Actor           string   `json:"actor"`
	Capabilities    []string `json:"capabilities"`
}

const mcpScopeFilename = "mcp-scope.json"

func mcpScopePath(dataRoot string) string {
	return filepath.Join(dataRoot, ".mdsys", "context", "v1", mcpScopeFilename)
}

// MCPScopePath exposes the marker location for the adapter and tests.
func MCPScopePath(dataRoot string) string { return mcpScopePath(dataRoot) }

// WriteMCPScope serializes scope with a stable ordering so the adapter's
// byte-for-byte comparison is deterministic.
func WriteMCPScope(dataRoot string, scope MCPScope) error {
	if scope.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("mcp scope protocol_version must be %q", ProtocolVersion)
	}
	if scope.Actor == "" {
		return fmt.Errorf("mcp scope actor must be set")
	}
	if len(scope.Capabilities) != 2 || scope.Capabilities[0] != string(CapabilitySearch) || scope.Capabilities[1] != string(CapabilityRead) {
		return fmt.Errorf("mcp scope capabilities must be exactly [search read]")
	}
	path := mcpScopePath(dataRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create context protocol directory: %w", err)
	}
	data, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", mcpScopeFilename, err)
	}
	return os.Chmod(path, 0o600)
}

// LoadMCPScope reads and validates the scope marker. It fails closed: any
// missing file, malformed JSON, unknown actor, or missing capability
// prevents the adapter from starting.
func LoadMCPScope(dataRoot string, policy Policy) (MCPScope, error) {
	path := mcpScopePath(dataRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return MCPScope{}, fmt.Errorf("refusing to start: read %s: %w", mcpScopeFilename, err)
	}
	var scope MCPScope
	if err := json.Unmarshal(data, &scope); err != nil {
		return MCPScope{}, fmt.Errorf("refusing to start: parse %s: %w", mcpScopeFilename, err)
	}
	if scope.ProtocolVersion != ProtocolVersion {
		return MCPScope{}, fmt.Errorf("refusing to start: %s protocol_version is %q", mcpScopeFilename, scope.ProtocolVersion)
	}
	actor, ok := policy.Actors[scope.Actor]
	if !ok {
		return MCPScope{}, fmt.Errorf("refusing to start: %s names actor %q which is not present in policy.json", mcpScopeFilename, scope.Actor)
	}
	for _, capability := range []string{string(CapabilitySearch), string(CapabilityRead)} {
		found := false
		for _, granted := range actor.Capabilities {
			if string(granted) == capability {
				found = true
				break
			}
		}
		if !found {
			return MCPScope{}, fmt.Errorf("refusing to start: actor %q does not hold capability %q in policy.json", scope.Actor, capability)
		}
	}
	return scope, nil
}
