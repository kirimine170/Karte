package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistributionBuildEmbedsDesktopClient(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	base := map[string]string{"GOFLAGS": "-buildvcs=false"}
	data := []byte(`{"installed":{"client_id":"karte-buildtest.apps.googleusercontent.com","client_secret":"build-test-secret"}}`)
	env, cleanup, err := withGoogleDriveOAuthBuildConfig(base, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if !strings.HasPrefix(env["GOFLAGS"], "-buildvcs=false ") || strings.Contains(env["GOFLAGS"], "build-test-secret") {
		t.Fatalf("unexpected GOFLAGS: %q", env["GOFLAGS"])
	}
	if base["GOFLAGS"] != "-buildvcs=false" || env["GOCACHE"] == "" {
		t.Fatal("expected the build cache to be isolated without changing base environment")
	}

	// Compile and run with the overlay, as Wails does when building the application.
	env["KARTE_GOOGLE_OAUTH_VERIFY_TEST_BUILD"] = "1"
	env["KARTE_GOOGLE_OAUTH_CLIENT_FILE"] = ""
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", "-run", "^TestVerifyDistributionBuild$", "-v", "./internal/googledrive")
	cmd.Env = mergeEnv(os.Environ(), env)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "PASS: TestVerifyDistributionBuild") {
		t.Fatalf("embedded client test: %v\n%s", err, output)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(env["GOCACHE"])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary client files or build cache were retained: %v", err)
	}
}

func TestDistributionBuildRejectsWebClientWithoutExposingInput(t *testing.T) {
	_, _, err := withGoogleDriveOAuthBuildConfig(nil, []byte(`{"web":{"client_id":"karte-buildtest.apps.googleusercontent.com","client_secret":"test-secret-marker"}}`))
	if err == nil || strings.Contains(err.Error(), "test-secret-marker") {
		t.Fatalf("expected a sanitized error for a web client, got %v", err)
	}
}
