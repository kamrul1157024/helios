package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Bind string `yaml:"bind"`
	Port int    `yaml:"port"`
}

type AuthConfig struct {
	Enabled   bool `yaml:"enabled"`
	SkipLocal bool `yaml:"skip_local"`
}

type DBConfig struct {
	Path string `yaml:"path"`
}

type Config struct {
	Server ServerConfig `yaml:"server"`
	Auth   AuthConfig   `yaml:"auth"`
	DB     DBConfig     `yaml:"db"`
}

func HeliosDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".helios")
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Bind: "localhost",
			Port: 7654,
		},
		Auth: AuthConfig{
			Enabled:   true,
			SkipLocal: true,
		},
		DB: DBConfig{
			Path: filepath.Join(HeliosDir(), "helios.db"),
		},
	}
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
