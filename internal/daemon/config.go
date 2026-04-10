package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Bind         string `yaml:"bind"`
	Port         int    `yaml:"port"`           // Deprecated: use InternalPort
	InternalPort int    `yaml:"internal_port"`
	PublicPort   int    `yaml:"public_port"`
}

type AuthConfig struct {
	Enabled   bool `yaml:"enabled"`
	SkipLocal bool `yaml:"skip_local"`
}

type TunnelConfig struct {
	Provider  string `yaml:"provider"`   // cloudflare | ngrok | tailscale | local | custom
	CustomURL string `yaml:"custom_url"` // only used when provider=custom
}

type DBConfig struct {
	Path string `yaml:"path"`
}

type Config struct {
	Server ServerConfig `yaml:"server"`
	Auth   AuthConfig   `yaml:"auth"`
	Tunnel TunnelConfig `yaml:"tunnel"`
	DB     DBConfig     `yaml:"db"`
}

func HeliosDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".helios")
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Bind:         "localhost",
			InternalPort: 7654,
			PublicPort:   7655,
		},
		Auth: AuthConfig{
			Enabled: true,
		},
		DB: DBConfig{
			Path: filepath.Join(HeliosDir(), "helios.db"),
		},
	}
}

func SaveConfig(cfg *Config) error {
	configPath := filepath.Join(HeliosDir(), "config.yaml")
	if err := os.MkdirAll(HeliosDir(), 0755); err != nil {
		return fmt.Errorf("create helios dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(configPath, data, 0644)
}

func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	configPath := filepath.Join(HeliosDir(), "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(HeliosDir(), 0755); err != nil {
				return nil, fmt.Errorf("create helios dir: %w", err)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}
