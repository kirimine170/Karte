package googledrive

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
)

const (
	credentialEnv = "KARTE_GOOGLE_OAUTH_CLIENT_FILE"
	maxClientJSON = 32 << 10
)

var (
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.apps\.googleusercontent\.com$`)
	errNoClient     = errors.New("この配布版には Google Drive の接続設定がありません。配布元にお問い合わせください")
)

// buildmatrix can overlay this empty default at build time for distribution.
//
//go:embed distribution_client.json
var distributionClientJSON []byte

type credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func loadCredentials() (credentials, error) {
	path := strings.TrimSpace(os.Getenv(credentialEnv))
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return credentials{}, errors.New("OAuthクライアントJSONを開けません")
		}
		defer file.Close()
		data, err := readBounded(file, maxClientJSON)
		if err != nil {
			return credentials{}, errors.New("OAuthクライアントJSONを読み取れません")
		}
		return parseDesktopClient(data)
	}
	if bytes.Equal(bytes.TrimSpace(distributionClientJSON), []byte("{}")) {
		return credentials{}, errNoClient
	}
	return parseDesktopClient(distributionClientJSON)
}

// ValidateDesktopClientJSON checks the same format accepted by the desktop app.
func ValidateDesktopClientJSON(data []byte) error {
	_, err := parseDesktopClient(data)
	return err
}

func parseDesktopClient(data []byte) (credentials, error) {
	var config struct {
		Installed credentials `json:"installed"`
	}
	if len(data) > maxClientJSON || json.Unmarshal(data, &config) != nil || !clientIDPattern.MatchString(config.Installed.ClientID) {
		return credentials{}, errors.New("GoogleのデスクトップOAuthクライアントJSONを指定してください")
	}
	return config.Installed, nil
}
