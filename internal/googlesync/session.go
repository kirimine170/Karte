// Package googlesync connects Karte to a user's private Drive application data.
package googlesync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	authURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL    = "https://oauth2.googleapis.com/token"
	userInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	driveScope  = "https://www.googleapis.com/auth/drive.appdata"
)

var ErrSignedOut = errors.New("Googleアカウントにログインしてください")
var errMissingCredential = errors.New("Google credential is absent")

type Config struct {
	ClientID     string
	ClientSecret string
}

type Status struct {
	Connected bool   `json:"connected"`
	Email     string `json:"email,omitempty"`
}

type credential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	ClientID     string    `json:"client_id"`
	Subject      string    `json:"subject"`
	Email        string    `json:"email"`
}

type credentialStore interface {
	load() ([]byte, error)
	save([]byte) error
	remove() error
}

type Session struct {
	mu                    sync.Mutex
	config                Config
	store                 credentialStore
	client                *http.Client
	authorizationEndpoint string
	tokenEndpoint         string
	profileEndpoint       string
}

func NewSession(config Config) *Session {
	return &Session{
		config: config, store: osCredentialStore{key: "Karte.GoogleDrive"},
		client: &http.Client{
			Timeout:       25 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		authorizationEndpoint: authURL, tokenEndpoint: tokenURL, profileEndpoint: userInfoURL,
	}
}

func (s *Session) read() (credential, error) {
	raw, err := s.store.load()
	if errors.Is(err, errMissingCredential) {
		return credential{}, ErrSignedOut
	}
	if err != nil {
		return credential{}, fmt.Errorf("OSのGoogle認証情報を読み込めません")
	}
	var value credential
	if err := json.Unmarshal(raw, &value); err != nil || value.AccessToken == "" ||
		value.RefreshToken == "" || value.Subject == "" || value.ClientID != s.config.ClientID {
		return credential{}, fmt.Errorf("Google認証情報を確認できません。再ログインしてください")
	}
	return value, nil
}

func (s *Session) save(value credential) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("Google認証情報を変換できません")
	}
	if err := s.store.save(raw); err != nil {
		return fmt.Errorf("Google認証情報をOSの保管場所に保存できません")
	}
	return nil
}

func (s *Session) Status() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.read()
	if errors.Is(err, ErrSignedOut) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return Status{Connected: true, Email: value.Email}, nil
}

func (s *Session) SignOut() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.remove(); err != nil && !errors.Is(err, errMissingCredential) {
		return fmt.Errorf("OSのGoogle認証情報を削除できません")
	}
	return nil
}

func (s *Session) SignIn(ctx context.Context, openBrowser func(string)) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.ClientID == "" || s.config.ClientSecret == "" {
		return Status{}, fmt.Errorf("KarteのGoogle OAuthクライアントが設定されていません")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	state, err := randomString()
	if err != nil {
		return Status{}, err
	}
	verifier, err := randomString()
	if err != nil {
		return Status{}, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Status{}, fmt.Errorf("Googleログインのローカル受信を開始できません")
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/oauth/callback"
	callback := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		query := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "Return to Karte to finish Google sign-in.")
		select {
		case callback <- query.Get("code"):
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	challenge := sha256.Sum256([]byte(verifier))
	params := url.Values{
		"response_type": {"code"}, "client_id": {s.config.ClientID},
		"redirect_uri": {redirect}, "scope": {"openid email " + driveScope},
		"state": {state}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"access_type":    {"offline"}, "prompt": {"consent select_account"},
	}
	openBrowser(s.authorizationEndpoint + "?" + params.Encode())

	var code string
	select {
	case code = <-callback:
	case <-ctx.Done():
	}
	if code == "" {
		return Status{}, fmt.Errorf("Googleログインを完了できませんでした")
	}
	value, err := s.exchange(ctx, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier},
	})
	if err != nil || value.RefreshToken == "" {
		return Status{}, fmt.Errorf("Googleのオフラインアクセスを許可できませんでした")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.profileEndpoint, nil)
	if err != nil {
		return Status{}, fmt.Errorf("Googleアカウントを確認できません")
	}
	request.Header.Set("Authorization", "Bearer "+value.AccessToken)
	var profile struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
	}
	if err := s.getJSON(request, &profile); err != nil || profile.Subject == "" {
		return Status{}, fmt.Errorf("Googleアカウントを確認できません")
	}
	value.Subject, value.Email = profile.Subject, profile.Email
	if err := s.save(value); err != nil {
		return Status{}, err
	}
	return Status{Connected: true, Email: value.Email}, nil
}

func (s *Session) authorization(ctx context.Context) (credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.read()
	if err != nil || time.Until(value.ExpiresAt) > time.Minute {
		return value, err
	}
	fresh, err := s.exchange(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {value.RefreshToken},
	})
	if err != nil {
		return credential{}, err
	}
	fresh.Subject, fresh.Email = value.Subject, value.Email
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = value.RefreshToken
	}
	if err := s.save(fresh); err != nil {
		return credential{}, err
	}
	return fresh, nil
}

func (s *Session) exchange(ctx context.Context, params url.Values) (credential, error) {
	params.Set("client_id", s.config.ClientID)
	params.Set("client_secret", s.config.ClientSecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenEndpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return credential{}, fmt.Errorf("Google認証リクエストを作成できません")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := s.getJSON(request, &response); err != nil || response.AccessToken == "" || response.ExpiresIn <= 0 {
		return credential{}, fmt.Errorf("Google認証を更新できません")
	}
	if response.Scope != "" && !strings.Contains(" "+response.Scope+" ", " "+driveScope+" ") {
		return credential{}, fmt.Errorf("Google Driveの同期権限が許可されていません")
	}
	return credential{
		AccessToken: response.AccessToken, RefreshToken: response.RefreshToken,
		ClientID: s.config.ClientID, ExpiresAt: time.Now().Add(time.Duration(response.ExpiresIn) * time.Second),
	}, nil
}

func (s *Session) getJSON(request *http.Request, target any) error {
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("Googleに接続できません")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Googleからエラー応答を受け取りました")
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target)
}

func randomString() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("Googleログインの初期化に失敗しました")
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

type osCredentialStore struct {
	key string
}
