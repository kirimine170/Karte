package googlesync

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type testCredentials struct {
	data []byte
}

func (m *testCredentials) load() ([]byte, error) {
	if m.data == nil {
		return nil, errMissingCredential
	}
	return m.data, nil
}
func (m *testCredentials) save(data []byte) error {
	m.data = append([]byte(nil), data...)
	return nil
}
func (m *testCredentials) remove() error {
	m.data = nil
	return nil
}

func TestDesktopOAuthAndCredentialLifecycle(t *testing.T) {
	config := Config{ClientID: "test-client", ClientSecret: "test-secret"}
	session := NewSession(config)
	vault := &testCredentials{}
	session.store = vault
	challenge := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil || r.Form.Get("client_id") != config.ClientID ||
			r.Form.Get("client_secret") != config.ClientSecret {
			t.Error("invalid OAuth client form")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") == "authorization_code" {
			digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "test-code" || base64.RawURLEncoding.EncodeToString(digest[:]) != <-challenge {
				t.Error("OAuth exchange did not bind the verifier")
			}
			_, _ = fmt.Fprint(w, `{"access_token":"test-access","refresh_token":"test-refresh","expires_in":3600}`)
			return
		}
		if r.Form.Get("refresh_token") != "test-refresh" {
			t.Error("refresh did not use the saved credential")
		}
		_, _ = fmt.Fprint(w, `{"access_token":"test-fresh","expires_in":3600}`)
	})
	mux.HandleFunc("GET /profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access" {
			t.Error("profile request did not authenticate")
		}
		_, _ = fmt.Fprint(w, `{"sub":"test-subject","email":"user@example.invalid"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	session.tokenEndpoint, session.profileEndpoint = server.URL+"/token", server.URL+"/profile"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := session.SignIn(ctx, func(auth string) {
		u, err := url.Parse(auth)
		if err != nil {
			t.Fatal(err)
		}
		query := u.Query()
		if query.Get("code_challenge_method") != "S256" || query.Get("access_type") != "offline" ||
			!strings.Contains(query.Get("scope"), driveScope) || query.Get("client_secret") != "" {
			t.Fatal("invalid browser authorization parameters")
		}
		challenge <- query.Get("code_challenge")
		redirect := query.Get("redirect_uri")
		for _, state := range []string{"wrong", query.Get("state")} {
			callback := redirect + "?" + url.Values{"state": {state}, "code": {"test-code"}}.Encode()
			response, err := http.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if (state == "wrong") != (response.StatusCode == http.StatusBadRequest) {
				t.Fatal("callback did not enforce state")
			}
		}
	})
	if err != nil || !status.Connected || status.Email != "user@example.invalid" {
		t.Fatalf("OAuth sign-in failed: %v", err)
	}
	public, err := json.Marshal(status)
	if err != nil || strings.Contains(string(public), "test-access") || strings.Contains(string(public), "test-refresh") {
		t.Fatal("public status exposed a credential")
	}

	value, err := session.read()
	if err != nil {
		t.Fatal(err)
	}
	value.ExpiresAt = time.Now().Add(-time.Hour)
	if err := session.save(value); err != nil {
		t.Fatal(err)
	}
	restarted := NewSession(config)
	restarted.store, restarted.tokenEndpoint = vault, session.tokenEndpoint
	fresh, err := restarted.authorization(ctx)
	if err != nil || fresh.AccessToken != "test-fresh" || fresh.RefreshToken != "test-refresh" {
		t.Fatalf("credential did not refresh after restart: %v", err)
	}
	if err := restarted.SignOut(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.authorization(ctx); !errors.Is(err, ErrSignedOut) {
		t.Fatal("credential remained after logout")
	}
}
