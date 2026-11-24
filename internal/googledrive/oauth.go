package googledrive

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
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	credentialEnv = "KARTE_GOOGLE_OAUTH_CLIENT_FILE"
	scope         = "https://www.googleapis.com/auth/drive.file"
	callbackPath  = "/oauth/callback"
	authTimeout   = 2 * time.Minute
)

var (
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.apps\.googleusercontent\.com$`)
	fileIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
	errDisconnected = errors.New("Google Driveに接続してください")
	errReauthorize  = errors.New("Google Driveの認可が失効しました。再接続してください")
)

type credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Type         string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	expiresAt    time.Time
}

type Service struct {
	mu           sync.Mutex
	authCancelMu sync.Mutex
	cancelAuth   context.CancelFunc
	http         *http.Client
	authURL      string
	tokenURL     string
	driveURL     string
	uploadURL    string
	client       credentials
	token        token
	account      Account
}

type Account struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"emailAddress"`
}

func New() *Service {
	return &Service{
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		authURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		tokenURL:  "https://oauth2.googleapis.com/token",
		driveURL:  "https://www.googleapis.com/drive/v3",
		uploadURL: "https://www.googleapis.com/upload/drive/v3",
	}
}

func loadCredentials() (credentials, error) {
	path := strings.TrimSpace(os.Getenv(credentialEnv))
	if path == "" {
		return credentials{}, fmt.Errorf("%s にGoogleのデスクトップOAuthクライアントJSONのパスを設定してください", credentialEnv)
	}
	f, err := os.Open(path)
	if err != nil {
		return credentials{}, errors.New("OAuthクライアントJSONを開けません")
	}
	defer f.Close()
	data, err := readBounded(f, 32<<10)
	if err != nil {
		return credentials{}, errors.New("OAuthクライアントJSONを読み取れません")
	}
	var config struct {
		Installed credentials `json:"installed"`
	}
	if json.Unmarshal(data, &config) != nil || !clientIDPattern.MatchString(config.Installed.ClientID) {
		return credentials{}, errors.New("GoogleのデスクトップOAuthクライアントJSONを指定してください")
	}
	return config.Installed, nil
}

func (s *Service) Authorize(parent context.Context, openBrowser func(string), pick bool) (Account, string, error) {
	s.authCancelMu.Lock()
	if s.cancelAuth != nil {
		s.authCancelMu.Unlock()
		return Account{}, "", errors.New("Google Driveの認可が既に進行中です")
	}
	ctx, cancel := context.WithTimeout(parent, authTimeout)
	s.cancelAuth = cancel
	s.authCancelMu.Unlock()
	defer func() {
		s.authCancelMu.Lock()
		s.cancelAuth = nil
		s.authCancelMu.Unlock()
		cancel()
	}()

	client, err := loadCredentials()
	if err != nil {
		return Account{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	callback, err := s.waitForCallback(ctx, client.ClientID, s.account.Email, openBrowser, pick)
	if err != nil {
		return Account{}, "", err
	}
	newToken, err := s.exchange(ctx, client, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {callback.code},
		"code_verifier": {callback.verifier},
		"redirect_uri":  {callback.redirect},
	}, true)
	if err != nil {
		return Account{}, "", err
	}
	var about struct {
		User Account `json:"user"`
	}
	query := url.Values{"fields": {"user(displayName,emailAddress)"}}
	body, err := s.request(ctx, newToken.AccessToken, http.MethodGet, s.driveURL+"/about?"+query.Encode(), "", nil, 32<<10)
	if err != nil {
		return Account{}, "", err
	}
	if err := json.Unmarshal(body, &about); err != nil || about.User.Email == "" {
		return Account{}, "", errors.New("接続したGoogleアカウントを確認できません")
	}
	s.client, s.token, s.account = client, newToken, about.User
	return s.account, callback.fileID, nil
}

func (s *Service) CancelAuthorization() {
	s.authCancelMu.Lock()
	defer s.authCancelMu.Unlock()
	if s.cancelAuth != nil {
		s.cancelAuth()
	}
}

func (s *Service) Disconnect() {
	s.CancelAuthorization()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearSession()
}

func (s *Service) clearSession() {
	s.client, s.token, s.account = credentials{}, token{}, Account{}
}

func (s *Service) Account() Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account
}

type authorizationCallback struct {
	code, verifier, redirect, fileID string
	err                              error
}

func (s *Service) waitForCallback(ctx context.Context, clientID, email string, openBrowser func(string), pick bool) (authorizationCallback, error) {
	state, verifier := randomURLToken(), randomURLToken()
	challenge := sha256.Sum256([]byte(verifier))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return authorizationCallback{}, errors.New("ローカルのOAuthコールバックを開始できません")
	}
	address := listener.Addr().String()
	redirect := "http://" + address + callbackPath
	results := make(chan authorizationCallback, 1)
	var used atomic.Bool
	server := &http.Server{
		ReadHeaderTimeout: 3 * time.Second,
		WriteTimeout:      5 * time.Second,
		MaxHeaderBytes:    16 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if r.Method != http.MethodGet || r.Host != address || r.URL.Path != callbackPath {
				http.NotFound(w, r)
				return
			}
			query, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
				http.Error(w, "Invalid authorization state.", http.StatusBadRequest)
				return
			}
			if !used.CompareAndSwap(false, true) {
				http.Error(w, "Authorization response already received.", http.StatusConflict)
				return
			}
			result := authorizationCallback{code: query.Get("code"), verifier: verifier, redirect: redirect}
			switch {
			case query.Get("error") == "access_denied":
				result.err = errors.New("Google Driveの認可がキャンセルされました")
			case query.Get("error") != "" || len(query["code"]) != 1 || result.code == "":
				result.err = errors.New("Google Driveの認可コードを取得できません")
			case pick:
				result.fileID = query.Get("picked_file_ids")
				if !fileIDPattern.MatchString(result.fileID) {
					result.err = errors.New("Google PickerでMarkdownファイルを1件選んでください")
				}
			}
			if result.err != nil {
				w.WriteHeader(http.StatusBadRequest)
			}
			_, _ = io.WriteString(w, "Karte received the browser response. Return to the desktop app to continue.")
			_ = http.NewResponseController(w).Flush()
			results <- result
		}),
	}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	params := url.Values{
		"client_id": {clientID}, "redirect_uri": {redirect},
		"response_type": {"code"}, "scope": {scope}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"},
		"access_type": {"offline"}, "prompt": {"consent select_account"},
	}
	if pick {
		params.Set("prompt", "consent")
		params.Set("trigger_onepick", "true")
		if email != "" {
			params.Set("login_hint", email)
		}
	}
	if ctx.Err() == nil {
		openBrowser(s.authURL + "?" + params.Encode())
	}
	select {
	case result := <-results:
		return result, result.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return authorizationCallback{}, errors.New("Google Driveの認可がタイムアウトしました")
		}
		return authorizationCallback{}, errors.New("Google Driveの認可がキャンセルされました")
	}
}

func randomURLToken() string {
	var data [32]byte
	_, _ = rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:])
}

func (s *Service) exchange(ctx context.Context, client credentials, form url.Values, checkScope bool) (token, error) {
	form.Set("client_id", client.ClientID)
	if client.ClientSecret != "" {
		form.Set("client_secret", client.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return token{}, errors.New("Google OAuthへのリクエストを作成できません")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return token{}, errors.New("Google OAuthに接続できません")
	}
	defer resp.Body.Close()
	body, err := readBounded(resp.Body, 32<<10)
	if err != nil {
		return token{}, errors.New("Google OAuthの応答を読み取れません")
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct{ Error string }
		_ = json.Unmarshal(body, &failure)
		if failure.Error == "invalid_grant" && !checkScope {
			return token{}, errReauthorize
		}
		return token{}, fmt.Errorf("Google OAuthのトークン取得に失敗しました (HTTP %d)", resp.StatusCode)
	}
	var next token
	if err := json.Unmarshal(body, &next); err != nil || next.AccessToken == "" ||
		!strings.EqualFold(next.Type, "Bearer") || next.ExpiresIn <= 0 || next.ExpiresIn > 86400 {
		return token{}, errors.New("Google OAuthのトークン応答が不正です")
	}
	if (checkScope || next.Scope != "") && strings.TrimSpace(next.Scope) != scope {
		return token{}, errors.New("Google Driveのファイル権限だけを許可したトークンが必要です")
	}
	next.expiresAt = time.Now().Add(time.Duration(next.ExpiresIn) * time.Second)
	return next, nil
}

// s.mu protects the token, including refresh and disconnect.
func (s *Service) accessToken(ctx context.Context) (string, error) {
	if s.token.AccessToken == "" {
		return "", errDisconnected
	}
	if time.Until(s.token.expiresAt) > 30*time.Second {
		return s.token.AccessToken, nil
	}
	if s.token.RefreshToken == "" {
		s.clearSession()
		return "", errReauthorize
	}
	next, err := s.exchange(ctx, s.client, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {s.token.RefreshToken},
	}, false)
	if err != nil {
		if errors.Is(err, errReauthorize) {
			s.clearSession()
		}
		return "", err
	}
	if next.RefreshToken == "" {
		next.RefreshToken = s.token.RefreshToken
	}
	s.token = next
	return s.token.AccessToken, nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(body)) > limit {
		err = errors.New("応答がサイズ上限を超えています")
	}
	return body, err
}
