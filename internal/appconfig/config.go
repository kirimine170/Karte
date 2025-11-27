package appconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config represents user-editable application settings (currently collaborative editing only).
type Config struct {
	Collaborative CollaborativeConfig `json:"collaborative"`
}

// CollaborativeConfig holds MQTT/presence related settings.
type CollaborativeConfig struct {
	Enabled   bool       `json:"enabled"`
	Workspace string     `json:"workspace"`
	UserName  string     `json:"userName"`
	ClientID  string     `json:"clientId"`
	MQTT      MQTTConfig `json:"mqtt"`
}

// MQTTConfig contains broker connection information.
type MQTTConfig struct {
	Broker           string `json:"broker"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	TLS              bool   `json:"tls"`
	SkipVerify       bool   `json:"skipVerify"`
	KeepAliveSeconds int    `json:"keepAliveSeconds"`
}

// Default returns a Config populated with safe defaults.
func Default() Config {
	return Config{
		Collaborative: CollaborativeConfig{
			Enabled:   false,
			Workspace: "HackSick",
			UserName:  "your-name",
			ClientID:  "",
			MQTT: MQTTConfig{
				Broker:           "mqtts://broker.example.com:8883",
				TLS:              true,
				SkipVerify:       false,
				KeepAliveSeconds: 30,
			},
		},
	}
}

// Load reads the config from disk. If the file does not exist it returns the default config.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if len(data) == 0 {
		return cfg, nil
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// Save writes the config to disk, creating parent directories when必要.
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("prepare config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// LoadOrCreate loads config, writing defaults if missing.
func LoadOrCreate(path string) (Config, bool, error) {
	cfg, err := Load(path)
	if err != nil {
		return cfg, false, err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := Save(path, cfg); err != nil {
			return cfg, false, err
		}
		return cfg, true, nil
	}

	return cfg, false, nil
}
