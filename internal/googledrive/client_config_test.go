package googledrive

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"testing"
)

func TestVerifyDistributionBuild(t *testing.T) {
	if os.Getenv("KARTE_GOOGLE_OAUTH_VERIFY_TEST_BUILD") != "1" {
		t.Skip("run by the buildmatrix overlay test")
	}
	t.Setenv(credentialEnv, "")
	const id = "karte-buildtest.apps.googleusercontent.com"
	client, err := loadCredentials()
	if err != nil || client.ClientID != id || client.ClientSecret != "build-test-secret" {
		t.Fatal("the build did not load the embedded desktop client")
	}
	var opened bool
	service := New()
	_, _, err = service.Authorize(t.Context(), func(rawURL string) {
		opened = true
		target, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		params := target.Query()
		if target.Scheme != "https" || target.Host != "accounts.google.com" || params.Get("client_id") != id ||
			params.Get("scope") != scope || params.Get("code_challenge_method") != "S256" || params.Has("client_secret") {
			t.Fatal("the built-in client did not open the correct Google authorization request")
		}
		assertCallback(t, params.Get("redirect_uri")+"?"+url.Values{
			"state": {params.Get("state")}, "error": {"access_denied"},
		}.Encode(), http.StatusBadRequest)
	}, false)
	if !opened || err == nil {
		t.Fatalf("expected the browser flow followed by cancellation, got opened=%t, error=%v", opened, err)
	}
}

func TestMissingClientRefersUserToPublisher(t *testing.T) {
	t.Setenv(credentialEnv, "")
	previous := distributionClientJSON
	distributionClientJSON = []byte("{}")
	t.Cleanup(func() { distributionClientJSON = previous })
	_, _, err := New().Authorize(t.Context(), func(string) { t.Fatal("opened browser without a client") }, false)
	if !errors.Is(err, errNoClient) {
		t.Fatalf("expected publisher setup error, got %v", err)
	}
}
