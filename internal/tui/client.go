package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

type healthResponse struct {
	Status       string `json:"status"`
	InternalPort string `json:"internal_port"`
	Pending      int    `json:"pending"`
	SSEClients   int    `json:"sse_clients"`
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
	Provider      string `json:"provider"`
	CustomURL     string `json:"custom_url,omitempty"`
	LocalPort     int    `json:"local_port,omitempty"`
	TailscaleMode string `json:"tailscale_mode,omitempty"`
}

type tunnelStartResponse struct {
	PublicURL       string `json:"public_url"`
	Message         string `json:"message"`
	RestartRequired bool   `json:"restart_required"`
}

func (c *client) tunnelStart(provider, customURL string, localPort int, tailscaleMode string) (*tunnelStartResponse, error) {
	body, _ := json.Marshal(tunnelStartRequest{
		Provider:      provider,
		CustomURL:     customURL,
		LocalPort:     localPort,
		TailscaleMode: tailscaleMode,
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

// deviceCreate asks the daemon for a pairing token.
//
// Every failure is reported. It used to ignore the status code and the decode
// error and return an empty token as success, and the setup screen shows its
// spinner precisely while the token is empty — so a daemon that answered 500,
// or answered with anything that was not the expected JSON, left "Generating
// pairing code..." turning for ever with nothing on screen to say why.
func (c *client) deviceCreate() (*deviceCreateResponse, error) {
	resp, err := c.httpClient.Post(c.baseURL+"/internal/device/create", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var r deviceCreateResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unreadable response: %w", err)
	}
	if r.Token == "" {
		// A well-formed answer with nothing in it. Treated as success before,
		// which is the shape that produced the endless spinner.
		return nil, fmt.Errorf("daemon returned no pairing token")
	}
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

type sessionInfo struct {
	SessionID       string  `json:"session_id"`
	CWD             string  `json:"cwd"`
	Project         string  `json:"project"`
	Title           *string `json:"title,omitempty"`
	Status          string  `json:"status"`
	Model           *string `json:"model,omitempty"`
	Terminal        *string `json:"terminal,omitempty"`
	LastEvent       *string `json:"last_event,omitempty"`
	LastEventAt     *string `json:"last_event_at,omitempty"`
	LastUserMessage *string `json:"last_user_message,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

func (s *sessionInfo) label() string {
	if s.Title != nil && *s.Title != "" {
		return *s.Title
	}
	if s.LastUserMessage != nil && *s.LastUserMessage != "" {
		return *s.LastUserMessage
	}
	return ""
}

type sessionsListResponse struct {
	Sessions []sessionInfo `json:"sessions"`
}

func (c *client) sessionsList() (*sessionsListResponse, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/internal/sessions")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r sessionsListResponse
	json.NewDecoder(resp.Body).Decode(&r)
	return &r, nil
}

func (c *client) sessionCreate(cwd string) (*sessionInfo, error) {
	body, _ := json.Marshal(map[string]string{"cwd": cwd})
	resp, err := c.httpClient.Post(c.baseURL+"/internal/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		SessionID string `json:"session_id"`
		Terminal  string `json:"terminal"`
		CWD       string `json:"cwd"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &sessionInfo{
		SessionID: r.SessionID,
		CWD:       r.CWD,
		Terminal:  &r.Terminal,
		Status:    "starting",
	}, nil
}

func (c *client) sessionStop(sessionID string) error {
	resp, err := c.httpClient.Post(c.baseURL+"/internal/sessions/"+sessionID+"/stop", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *client) sessionTerminate(sessionID string) error {
	resp, err := c.httpClient.Post(c.baseURL+"/internal/sessions/"+sessionID+"/terminate", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *client) sessionResume(sessionID string) error {
	resp, err := c.longHTTPClient.Post(c.baseURL+"/internal/sessions/"+sessionID+"/resume", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *client) eventsURL() string {
	return c.baseURL + "/internal/events"
}

type settingsResponse struct {
	Settings map[string]string `json:"settings"`
}

func (c *client) getSettings() (map[string]string, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/internal/settings")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r settingsResponse
	json.NewDecoder(resp.Body).Decode(&r)
	if r.Settings == nil {
		r.Settings = map[string]string{}
	}
	return r.Settings, nil
}

func (c *client) updateSettings(settings map[string]string) error {
	body, _ := json.Marshal(settings)
	resp, err := c.httpClient.Do(func() *http.Request {
		req, _ := http.NewRequest(http.MethodPut, c.baseURL+"/internal/settings", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return req
	}())
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ─── Schedules ──────────────────────────────────────────────────────────────

// scheduleInfo is a schedule as the internal API reports it. Only the fields
// the list draws: the TUI shows a schedule, it does not edit one.
type scheduleInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	Cron        string `json:"cron"`
	AfterID     string `json:"after_id"`
	AfterWhen   string `json:"after_when"`
	Mode        string `json:"mode"`
	CWD         string `json:"cwd"`
	CheckCmd    string `json:"check_cmd"`
	CheckFile   string `json:"check_file"`
	NextRunAt   string `json:"next_run_at"`
	LastFiredAt string `json:"last_fired_at"`
	LastStatus  string `json:"last_status"`
	LastError   string `json:"last_error"`
	DoneAt      string `json:"done_at"`
	FailStreak  int    `json:"fail_streak"`
	FiresToday  int    `json:"fires_today"`
}

// state is where this schedule stands right now, in one word.
func (s scheduleInfo) state() string {
	switch {
	case s.LastStatus == "running":
		return "running"
	case s.LastStatus == "missed":
		return "missed"
	case s.LastStatus == "blocked":
		return "blocked"
	case s.DoneAt != "":
		return "done"
	case !s.Enabled:
		return "paused"
	case s.Kind == "after":
		return "waiting"
	case s.LastStatus == "failed":
		if s.FailStreak > 1 {
			return fmt.Sprintf("failed ×%d", s.FailStreak)
		}
		return "failed"
	}
	return untilWords(s.NextRunAt)
}

// summary is the second line: what it does, in words rather than in cron.
func (s scheduleInfo) summary() string {
	where := s.CWD
	if where == "" {
		where = "home"
	}
	switch s.Kind {
	case "monitor":
		check := s.CheckCmd
		if check == "" {
			check = s.CheckFile
		}
		return fmt.Sprintf("%s · %s", s.Cron, check)
	case "once":
		return "once · " + where
	case "after":
		when := "on success"
		if s.AfterWhen == "any" {
			when = "either way"
		}
		return when + " · " + where
	default:
		return fmt.Sprintf("%s · %s", s.Cron, where)
	}
}

// untilWords is how far off something is, in words a person reads at a glance.
func untilWords(stamp string) string {
	if stamp == "" {
		return "—"
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return "—"
	}
	until := time.Until(at)
	switch {
	case until < 0:
		return "due"
	case until < time.Minute:
		return fmt.Sprintf("in %ds", int(until.Seconds()))
	case until < time.Hour:
		return fmt.Sprintf("in %dm", int(until.Minutes()))
	case until < 12*time.Hour:
		return fmt.Sprintf("in %dh %dm", int(until.Hours()), int(until.Minutes())%60)
	default:
		return at.Local().Format("Mon 15:04")
	}
}

func (c *client) scheduleList() ([]scheduleInfo, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/internal/schedules")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("schedules: %s", resp.Status)
	}
	var r struct {
		Schedules []scheduleInfo `json:"schedules"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return r.Schedules, nil
}

// scheduleAct is every one-shot action the list offers: they differ only in the
// path and the body, and each is one line at the call site.
func (c *client) scheduleAct(method, path string, body any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.longHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var out struct {
			Message string `json:"message"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		if out.Message != "" {
			return fmt.Errorf("%s", out.Message)
		}
		return fmt.Errorf("%s", resp.Status)
	}
	return nil
}

func loadSchedules(c *client) tea.Cmd {
	return func() tea.Msg {
		list, err := c.scheduleList()
		return schedulesLoaded{schedules: list, err: err}
	}
}

func runSchedule(c *client, id string) tea.Cmd {
	return func() tea.Msg {
		return scheduleActed{err: c.scheduleAct(http.MethodPost, "/internal/schedules/"+id+"/run", nil)}
	}
}

func checkSchedule(c *client, id string) tea.Cmd {
	return func() tea.Msg {
		return scheduleActed{err: c.scheduleAct(http.MethodPost, "/internal/schedules/"+id+"/check", nil)}
	}
}

func setScheduleEnabled(c *client, id string, enabled bool) tea.Cmd {
	return func() tea.Msg {
		return scheduleActed{err: c.scheduleAct(http.MethodPatch, "/internal/schedules/"+id,
			map[string]any{"enabled": enabled})}
	}
}

func deleteSchedule(c *client, id string) tea.Cmd {
	return func() tea.Msg {
		return scheduleActed{err: c.scheduleAct(http.MethodDelete, "/internal/schedules/"+id, nil)}
	}
}
