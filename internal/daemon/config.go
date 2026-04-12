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
	Provider     string             `yaml:"provider"`      // cloudflare | ngrok | tailscale | local | custom | zrok | localtunnel | localhostrun | localxpose
	CustomURL    string             `yaml:"custom_url"`    // only used when provider=custom
	Zrok         ZrokConfig         `yaml:"zrok"`          // zrok-specific settings
	Localtunnel  LocaltunnelConfig  `yaml:"localtunnel"`   // localtunnel-specific settings
	LocalhostRun LocalhostRunConfig `yaml:"localhostrun"`  // localhost.run-specific settings
	Localxpose   LocalxposeConfig   `yaml:"localxpose"`    // localxpose-specific settings
}

type ZrokConfig struct {
	ShareMode  string `yaml:"share_mode"`  // public | reserved (default: reserved)
	ShareToken string `yaml:"share_token"` // reserved share token (auto-populated)
}

type LocaltunnelConfig struct {
	Subdomain string `yaml:"subdomain"` // requested subdomain (empty = random)
	Host      string `yaml:"host"`      // custom server URL (empty = default loca.lt)
}

type LocalhostRunConfig struct {
	SSHUser           string `yaml:"ssh_user"`           // "" | "nokey" (anonymous) | "plan" (custom domain)
	CustomDomain      string `yaml:"custom_domain"`      // custom domain (e.g., "myapp.lhr.rocks")
	KeepaliveInterval int    `yaml:"keepalive_interval"` // ServerAliveInterval in seconds (default: 60)
	UseAutossh        bool   `yaml:"use_autossh"`        // use autossh for auto-reconnect if available
}

type LocalxposeConfig struct {
	Subdomain      string `yaml:"subdomain"`       // ephemeral subdomain
	ReservedDomain string `yaml:"reserved_domain"`  // reserved domain (e.g., "my-helios.loclx.io")
	Region         string `yaml:"region"`           // us | eu | ap
	BasicAuth      string `yaml:"basic_auth"`       // user:pass for built-in auth
	AccessToken    string `yaml:"access_token"`     // access token (overrides loclx account login)
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
