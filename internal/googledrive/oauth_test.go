package googledrive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopAuthorizationAndRefresh(t *testing.T) {
	t.Setenv("KARTE_GOOGLE_OAUTH_EXPECTED_ACCOUNT", "USER@example.com")
	setDesktopCredentials(t, `{"installed":{"client_id":"karte-test.apps.googleusercontent.com","client_secret":"test-secret","token_uri":"https://ignored.invalid"}}`)
	authorization := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				http.Error(w, "invalid form", 400)
				return
			}
			if r.Method != "POST" || r.Form.Get("client_id") != "karte-test.apps.googleusercontent.com" || r.Form.Get("client_secret") != "test-secret" {
				t.Errorf("invalid token request: %v", r.Form)
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				params := <-authorization
				verifier := r.Form.Get("code_verifier")
				challenge := sha256.Sum256([]byte(verifier))
				if r.Form.Get("code") != "test-code" || r.Form.Get("redirect_uri") != params.Get("redirect_uri") ||
					base64.RawURLEncoding.EncodeToString(challenge[:]) != params.Get("code_challenge") || len(verifier) < 43 {
					t.Error("token request did not match the authorization code, redirect and PKCE challenge")
				}
				_ = json.NewEncoder(w).Encode(token{AccessToken: "access", RefreshToken: "refresh", Scope: scope, Type: "Bearer", ExpiresIn: 3600})
			case "refresh_token":
				if r.Form.Get("refresh_token") != "refresh" {
					t.Error("wrong refresh token")
				}
				_ = json.NewEncoder(w).Encode(token{AccessToken: "renewed", Type: "Bearer", ExpiresIn: 3600})
			default:
				t.Error("wrong grant type")
				http.Error(w, "invalid grant type", 400)
			}
		case "/about":
			if r.Header.Get("Authorization") != "Bearer access" || r.URL.Query().Get("fields") != "user(displayName,emailAddress)" {
				t.Error("Google account lookup used the wrong token or fields")
			}
			_, _ = io.WriteString(w, `{"user":{"displayName":"Test user","emailAddress":"user@example.com"}}`)
		case "/files":
			if r.Header.Get("Authorization") != "Bearer renewed" {
				t.Error("Drive did not use the refreshed access token")
			}
			if strings.Contains(r.URL.Query().Get("q"), "'root' in parents") {
				_, _ = io.WriteString(w, `{"files":[{"id":"karte-folder","name":"Karte","mimeType":"application/vnd.google-apps.folder","capabilities":{"canAddChildren":true}}]}`)
			} else {
				_, _ = io.WriteString(w, `{"files":[{"id":"sample","name":"sample.md","mimeType":"text/markdown","parents":["karte-folder"]}]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := New()
	service.tokenURL, service.driveURL = server.URL+"/token", server.URL
	account, id, err := service.Authorize(t.Context(), func(rawURL string) {
		target, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		params := target.Query()
		authorization <- params
		if target.Host != "accounts.google.com" || params.Get("scope") != scope ||
			params.Get("access_type") != "offline" || params.Get("code_challenge_method") != "S256" ||
			params.Get("trigger_onepick") != "true" || params.Get("prompt") != "consent" || params.Get("login_hint") != "USER@example.com" {
			t.Errorf("unexpected authorization URL: %s", rawURL)
		}
		redirect := params.Get("redirect_uri")
		if !strings.HasPrefix(redirect, "http://127.0.0.1:") {
			t.Fatalf("expected a loopback redirect, got %s", redirect)
		}
		assertCallback(t, redirect+"?code=untrusted&state=wrong", http.StatusBadRequest)
		assertCallback(t, redirect+"?"+url.Values{
			"code": {"test-code"}, "state": {params.Get("state")}, "picked_file_ids": {"sample"},
		}.Encode(), http.StatusOK)
		assertCallback(t, redirect+"?code=replay&state="+params.Get("state"), http.StatusConflict)
	}, true)
	if err != nil || id != "sample" || account.Email != "user@example.com" {
		t.Fatalf("authorization returned id=%q account=%+v error=%v", id, account, err)
	}

	service.token.expiresAt = time.Now().Add(-time.Second)
	files, err := service.List(t.Context())
	if err != nil || len(files) != 1 || files[0].ID != id || service.token.RefreshToken != "refresh" {
		t.Fatalf("list after refresh returned %+v, %v", files, err)
	}
	service.Disconnect()
	if service.Account().Email != "" {
		t.Fatal("disconnect retained account")
	}
	if _, err := service.List(t.Context()); !errors.Is(err, errDisconnected) {
		t.Fatalf("list after disconnect returned %v", err)
	}
}

func TestAuthorizationCancellationAndConfiguredClientType(t *testing.T) {
	setDesktopCredentials(t, `{"web":{"client_id":"karte-test.apps.googleusercontent.com"}}`)
	service := New()
	if _, _, err := service.Authorize(t.Context(), func(string) { t.Fatal("browser opened for Web application credential") }, false); err == nil {
		t.Fatal("accepted a non-desktop credential")
	}

	setDesktopCredentials(t, `{"installed":{"client_id":"karte-test.apps.googleusercontent.com"}}`)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err := service.Authorize(ctx, func(rawURL string) {
		target, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if target.Query().Has("trigger_onepick") {
			t.Error("normal connection launched Picker")
		}
		service.CancelAuthorization()
	}, false)
	if err == nil || !strings.Contains(err.Error(), "キャンセル") {
		t.Fatalf("cancel returned %v", err)
	}
	if service.Account().Email != "" {
		t.Fatal("cancellation opened a session")
	}
}

func TestUnexpectedAccountCannotCreateFolder(t *testing.T) {
	setDesktopCredentials(t, `{"installed":{"client_id":"karte-test.apps.googleusercontent.com"}}`)
	t.Setenv("KARTE_GOOGLE_OAUTH_EXPECTED_ACCOUNT", "allowed@example.com")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(token{AccessToken: "access", Scope: scope, Type: "Bearer", ExpiresIn: 3600})
		case "/about":
			_, _ = io.WriteString(w, `{"user":{"emailAddress":"different@example.com"}}`)
		default:
			t.Errorf("issued a Drive request for the unexpected account: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := New()
	service.tokenURL, service.driveURL = server.URL+"/token", server.URL
	_, _, err := service.Authorize(t.Context(), func(rawURL string) {
		target, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		params := target.Query()
		assertCallback(t, params.Get("redirect_uri")+"?"+url.Values{"state": {params.Get("state")}, "code": {"test-code"}}.Encode(), http.StatusOK)
	}, false)
	if err == nil || service.Account().Email != "" {
		t.Fatalf("created a session for the unexpected account: %v", err)
	}
	if _, err := service.Folder(t.Context()); !errors.Is(err, errDisconnected) {
		t.Fatalf("expected folder access to be blocked without an authorized session, got %v", err)
	}
}

func TestTokenScopeAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		body           string
		expired        bool
		accountCleared bool
	}{
		{"broader scope", 200, `{"access_token":"token","scope":"https://www.googleapis.com/auth/drive","token_type":"Bearer","expires_in":3600}`, false, false},
		{"revoked refresh token", 400, `{"error":"invalid_grant","error_description":"raw test secret"}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			service := New()
			service.tokenURL = server.URL
			service.account = Account{Email: "user@example.com"}
			service.token = token{AccessToken: "test-access", RefreshToken: "test-refresh"}
			_, err := service.exchange(t.Context(), credentials{}, url.Values{}, !tc.expired)
			if err == nil || strings.Contains(err.Error(), "raw test secret") {
				t.Fatalf("expected a sanitized error, got %v", err)
			}
			if tc.expired {
				_, err = service.accessToken(t.Context())
				if !errors.Is(err, errReauthorize) {
					t.Fatalf("expected reauthorization, got %v", err)
				}
			}
			if (service.Account().Email == "") != tc.accountCleared {
				t.Fatal("unexpected session state")
			}
		})
	}
}

func setDesktopCredentials(t *testing.T, data string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(credentialEnv, file)
}

func assertCallback(t *testing.T, target string, status int) {
	t.Helper()
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Errorf("callback returned %d, want %d", response.StatusCode, status)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Error("callback did not prevent caching")
	}
}
