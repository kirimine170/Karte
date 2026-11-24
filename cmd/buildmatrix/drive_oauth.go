package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"karte/internal/googledrive"
)

const distributionClientEnv = "KARTE_GOOGLE_OAUTH_DISTRIBUTION_CLIENT_JSON"

func withGoogleDriveOAuthBuildConfig(base map[string]string, data []byte) (map[string]string, func(), error) {
	if len(data) == 0 {
		return base, func() {}, nil
	}
	if err := googledrive.ValidateDesktopClientJSON(data); err != nil {
		return nil, nil, fmt.Errorf("invalid %s: %w", distributionClientEnv, err)
	}
	source, err := filepath.Abs(filepath.Join("internal", "googledrive", "distribution_client.json"))
	if err != nil {
		return nil, nil, err
	}
	temp, err := os.MkdirTemp("", "karte-oauth-build-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }

	overlay := filepath.Join(temp, "overlay.json")
	if err := writeOAuthBuildOverlay(temp, source, overlay, data); err != nil {
		cleanup()
		return nil, nil, err
	}
	flag, err := quoteGoFlag("-overlay=" + overlay)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	flags, ok := base["GOFLAGS"]
	if !ok {
		flags = os.Getenv("GOFLAGS")
	}
	env := copyEnvMap(base)
	env["GOFLAGS"] = strings.TrimSpace(flags + " " + flag)
	// The compiled package includes the client configuration. Keep it out of the shared Go cache.
	env["GOCACHE"] = filepath.Join(temp, "go-cache")
	return env, cleanup, nil
}

func writeOAuthBuildOverlay(temp, source, overlay string, data []byte) error {
	client := filepath.Join(temp, "client.json")
	if err := os.WriteFile(client, data, 0o600); err != nil {
		return err
	}
	config := struct{ Replace map[string]string }{map[string]string{source: client}}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(overlay, encoded, 0o600)
}

func quoteGoFlag(flag string) (string, error) {
	if !strings.Contains(flag, "'") {
		return "'" + flag + "'", nil
	}
	if !strings.Contains(flag, "\"") {
		return "\"" + flag + "\"", nil
	}
	return "", fmt.Errorf("the temporary path cannot be quoted in GOFLAGS")
}
