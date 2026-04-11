package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// client wraps HTTP calls to the internal admin API.
type client struct {
	baseURL        string
	httpClient     *http.Client // short timeout for health/list
	longHTTPClient *http.Client // long timeout for tunnel start
}

func newClient(internalPort int) *client {
	return &client{
		baseURL:        fmt.Sprintf("http://127.0.0.1:%d", internalPort),
		httpClient:     &http.Client{Timeout: 3 * time.Second},
		longHTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

type tmuxHealthStatus struct {
	Installed       bool   `json:"installed"`
	Version         string `json:"version"`
	ServerRunning   bool   `json:"server_running"`
	ResurrectPlugin bool   `json:"resurrect_plugin"`
	ContinuumPlugin bool   `json:"continuum_plugin"`
}

type healthResponse struct {
	Status       string           `json:"status"`
	InternalPort string           `json:"internal_port"`
	Pending      int              `json:"pending"`
	SSEClients   int              `json:"sse_clients"`
	Tmux         *tmuxHealthStatus `json:"tmux,omitempty"`
}

func (c *client) health() (*healthResponse, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/internal/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r healthResponse
	json.NewDecoder(resp.Body).Decode(&r)
	return &r, nil
}

type tunnelStatusResponse struct {
	Active    bool   `json:"active"`
	Provider  string `json:"provider"`
	PublicURL string `json:"public_url"`
}

func (c *client) tunnelStatus() (*tunnelStatusResponse, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/internal/tunnel/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r tunnelStatusResponse
	json.NewDecoder(resp.Body).Decode(&r)
	return &r, nil
}

type tunnelStartRequest struct {
	Provider  string `json:"provider"`
	CustomURL string `json:"custom_url,omitempty"`
	LocalPort int    `json:"local_port,omitempty"`
}

type tunnelStartResponse struct {
	PublicURL string `json:"public_url"`
	Message   string `json:"message"`
}

func (c *client) tunnelStart(provider, customURL string, localPort int) (*tunnelStartResponse, error) {
	body, _ := json.Marshal(tunnelStartRequest{
		Provider:  provider,
		CustomURL: customURL,
		LocalPort: localPort,
	})
	resp, err := c.longHTTPClient.Post(c.baseURL+"/internal/tunnel/start", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r tunnelStartResponse
	json.Unmarshal(data, &r)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", r.Message)
	}
	return &r, nil
}

func (c *client) tunnelStop() error {
	resp, err := c.httpClient.Post(c.baseURL+"/internal/tunnel/stop", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

type deviceCreateResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
	SetupURL  string `json:"setup_url"`
}

func (c *client) deviceCreate() (*deviceCreateResponse, error) {
	resp, err := c.httpClient.Post(c.baseURL+"/internal/device/create", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r deviceCreateResponse
	json.NewDecoder(resp.Body).Decode(&r)
	return &r, nil
}

type deviceInfo struct {
	KID         string  `json:"kid"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Platform    string  `json:"platform"`
	Browser     string  `json:"browser"`
	PushEnabled bool    `json:"push_enabled"`
	LastSeenAt  *string `json:"last_seen_at"`
}

type deviceListResponse struct {
	Devices []deviceInfo `json:"devices"`
}

func (c *client) deviceList() (*deviceListResponse, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/internal/device/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r deviceListResponse
	json.NewDecoder(resp.Body).Decode(&r)
	return &r, nil
}

func (c *client) deviceActivate(kid string) error {
	body, _ := json.Marshal(map[string]string{"kid": kid})
	resp, err := c.httpClient.Post(c.baseURL+"/internal/device/activate", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *client) deviceRevoke(kid string) error {
	body, _ := json.Marshal(map[string]string{"kid": kid})
	resp, err := c.httpClient.Post(c.baseURL+"/internal/device/revoke", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
