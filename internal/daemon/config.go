package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Bind         string `yaml:"bind"`
	Port         int    `yaml:"port"` // Deprecated: use InternalPort
	InternalPort int    `yaml:"internal_port"`
	PublicPort   int    `yaml:"public_port"`
}

type AuthConfig struct {
	Enabled   bool `yaml:"enabled"`
	SkipLocal bool `yaml:"skip_local"`
}

type TunnelConfig struct {
	Provider     string             `yaml:"provider"`     // cloudflare | ngrok | tailscale | local | custom | zrok | localtunnel | localhostrun | localxpose
	CustomURL    string             `yaml:"custom_url"`   // only used when provider=custom
	Zrok         ZrokConfig         `yaml:"zrok"`         // zrok-specific settings
	Localtunnel  LocaltunnelConfig  `yaml:"localtunnel"`  // localtunnel-specific settings
	LocalhostRun LocalhostRunConfig `yaml:"localhostrun"` // localhost.run-specific settings
	Localxpose   LocalxposeConfig   `yaml:"localxpose"`   // localxpose-specific settings
	Tailscale    TailscaleConfig    `yaml:"tailscale"`    // tailscale-specific settings
}

// TailscaleConfig selects how Tailscale exposes the public server.
//
// Serve keeps traffic inside the tailnet, where WireGuard already provides
// end-to-end encryption, so it runs plain HTTP and needs no certificate.
// Funnel exposes the server publicly and must terminate TLS, which requires
// HTTPS certificates enabled for the tailnet.
type TailscaleConfig struct {
	Mode string `yaml:"mode"` // serve | funnel (default: serve)
	Port int    `yaml:"port"` // serve: any port (default 7655); funnel: 443 | 8443 | 10000 (default 443)
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
	ReservedDomain string `yaml:"reserved_domain"` // reserved domain (e.g., "my-helios.loclx.io")
	Region         string `yaml:"region"`          // us | eu | ap
	BasicAuth      string `yaml:"basic_auth"`      // user:pass for built-in auth
	AccessToken    string `yaml:"access_token"`    // access token (overrides loclx account login)
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

// ResolvePublicBind determines the interface the public server listens on.
//
// Every tunnel provider except "local" proxies from loopback, so exposing the
// public port on all interfaces hands out LAN access nobody asked for. The
// "local" provider is the exception: it hands out a LAN URL, so it needs one.
// An explicit bind in config or on the command line always wins; "localhost"
// is treated as the unset default, since that is what DefaultConfig writes.
func ResolvePublicBind(cfg *Config) string {
	if bind := strings.TrimSpace(cfg.Server.Bind); bind != "" && bind != "localhost" {
		return bind
	}
	if cfg.Tunnel.Provider == "local" {
		return "0.0.0.0"
	}
	return "127.0.0.1"
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
