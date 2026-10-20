package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/kamrul1157024/helios/internal/desktop"
)

func asClose(err error, ce *websocket.CloseError) bool { return errors.As(err, ce) }

// checker runs the checks in order and stops at the first failure, because
// each one stands on the ones before it.
type checker struct {
	c         *client
	container string
	passed    int
	started   time.Time
	sseEvents atomic.Int64
}

func (k *checker) step(name string, fn func() error) error {
	t := time.Now()
	if err := fn(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	k.passed++
	fmt.Printf("PASS %-58s %6.1fs\n", name, time.Since(t).Seconds())
	return nil
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	keyPath := fs.String("key", "test/e2e-desktop/.out/key.json", "paired device key")
	container := fs.String("container", "helios-e2e-desktop", "container the daemon runs in")
	fs.Parse(args)

	c, err := loadClient(*keyPath)
	if err != nil {
		return err
	}
	k := &checker{c: c, container: *container, started: time.Now()}
	go k.watchSSE()

	var first *viewer
	steps := []struct {
		name string
		fn   func() error
	}{
		{"state is missing before install", k.checkMissing},
		{"install runs every step to ready (real apt)", k.checkInstall},
		{"desktop_state events arrived over SSE", k.checkSSE},
		{"tunnel opens at 1280x800 and draws a frame", func() error {
			v, err := k.checkFirstViewer()
			first = v
			return err
		}},
		{"live resize to 1600x900 reaches guacd", func() error { return k.checkResize(first) }},
		{"a second viewer joins the same session", k.checkSecondViewer},
		{"PUT display dpi 144 reaches xfconf", k.checkDPI},
		{"used ticket gets 4401", k.checkUsedTicket},
		{"expired ticket gets 4401", k.checkExpiredTicket},
		{"5th viewer gets 4429", func() error { return k.checkViewerCap(first) }},
		{"xrdp and guacd listen on 127.0.0.1 only", k.checkListeners},
		{"agent session gets DISPLAY and xdpyinfo works", k.checkAgentDisplay},
	}
	for _, s := range steps {
		if err := k.step(s.name, s.fn); err != nil {
			return err
		}
	}
	fmt.Printf("\nAll %d checks passed in %s.\n", k.passed, time.Since(k.started).Round(time.Second))
	return nil
}

func (k *checker) status() (desktop.Status, error) {
	var st desktop.Status
	code, err := k.c.do("GET", "/api/desktop", nil, &st)
	if err == nil && code != 200 {
		err = fmt.Errorf("GET /api/desktop: HTTP %d", code)
	}
	return st, err
}

func (k *checker) checkMissing() error {
	st, err := k.status()
	if err != nil {
		return err
	}
	if st.State != desktop.StateMissing || !st.CanInstall || !st.Enabled {
		return fmt.Errorf("status %+v", st)
	}
	if strings.Join(st.Missing, ",") != "xrdp,xfce,guacd" {
		return fmt.Errorf("missing = %v", st.Missing)
	}
	return nil
}

func (k *checker) checkInstall() error {
	var started map[string]any
	code, err := k.c.do("POST", "/api/desktop/install", nil, &started)
	if err != nil || code != http.StatusAccepted {
		return fmt.Errorf("POST install: %d %v %v", code, started, err)
	}
	var busy map[string]any
	if code, _ := k.c.do("POST", "/api/desktop/install", nil, &busy); code != http.StatusConflict || busy["code"] != "busy" {
		return fmt.Errorf("second POST install: %d %v, want 409 busy", code, busy)
	}

	last := ""
	deadline := time.Now().Add(25 * time.Minute)
	for time.Now().Before(deadline) {
		st, err := k.status()
		if err != nil {
			return err
		}
		line := string(st.State)
		for _, s := range st.Steps {
			line += fmt.Sprintf(" %s=%s", s.ID, s.State)
		}
		if line != last {
			fmt.Printf("     %s\n", line)
			last = line
		}
		switch st.State {
		case desktop.StateReady:
			for _, s := range st.Steps {
				if s.State != desktop.StepDone {
					return fmt.Errorf("ready with step %s %s", s.ID, s.State)
				}
			}
			if st.Display == "" || len(st.Missing) != 0 {
				return fmt.Errorf("ready status %+v", st)
			}
			fmt.Printf("     display %s, dpi %d\n", st.Display, st.DPI)
			return nil
		case desktop.StateFailed:
			return fmt.Errorf("install failed: %s", st.Error)
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("install did not finish in 25 minutes")
}

// watchSSE counts desktop_state events on the stream every client keeps.
func (k *checker) watchSSE() {
	req, err := http.NewRequestWithContext(context.Background(), "GET", k.c.base+"/api/events", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+k.c.token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		if name, ok := strings.CutPrefix(line, "event: "); ok {
			event = name
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || event != "desktop_state" {
			continue
		}
		var st desktop.Status
		if json.Unmarshal([]byte(data), &st) == nil && st.State != "" {
			k.sseEvents.Add(1)
		}
		event = ""
	}
}

func (k *checker) checkSSE() error {
	n := k.sseEvents.Load()
	if n < 6 {
		return fmt.Errorf("only %d desktop_state events", n)
	}
	fmt.Printf("     %d desktop_state events\n", n)
	return nil
}

func (k *checker) checkFirstViewer() (*viewer, error) {
	// The extra parameters are what a hostile client would add. The daemon
	// must not read them: the session that answers is still the local one.
	v, err := k.c.openViewer(1280, 800, 96, "hostname=203.0.113.9&port=22&protocol=vnc&username=root")
	if err != nil {
		return nil, err
	}
	if _, seen, err := v.waitFor(60*time.Second, isScreenSize(1280, 800)); err != nil {
		return v, fmt.Errorf("size 1280x800: %w", err)
	} else if seen["error"] > 0 {
		return v, fmt.Errorf("guacd error before size: %v", seen)
	}
	_, seen, err := v.waitFor(30*time.Second, func(ins desktop.Instruction) bool {
		return ins.Opcode == "sync"
	})
	if err != nil {
		return v, fmt.Errorf("first frame: %w", err)
	}
	fmt.Printf("     first frame: %v\n", seen)
	return v, nil
}

func (k *checker) checkResize(v *viewer) error {
	if err := v.send("size", "1600", "900"); err != nil {
		return err
	}
	_, seen, err := v.waitFor(30*time.Second, isScreenSize(1600, 900))
	if err != nil {
		return err
	}
	fmt.Printf("     after resize: %v\n", seen)
	return nil
}

func (k *checker) checkSecondViewer() error {
	v, err := k.c.openViewer(1280, 800, 96, "")
	if err != nil {
		return err
	}
	defer v.close()
	_, _, err = v.waitFor(60*time.Second, func(ins desktop.Instruction) bool { return ins.Opcode == "sync" })
	if err != nil {
		return err
	}
	st, err := k.status()
	if err != nil {
		return err
	}
	if st.Viewers != 2 {
		return fmt.Errorf("viewers = %d, want 2", st.Viewers)
	}
	// One session, not two: sesman must have given both logins the same
	// display, which is what Policy=UB is for.
	out, err := k.exec("sh", "-c", "pgrep -u dev -f 'Xorg :' | wc -l")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "1" {
		return fmt.Errorf("%s X servers for dev, want 1", strings.TrimSpace(out))
	}
	return nil
}

func (k *checker) checkDPI() error {
	var bad map[string]any
	if code, _ := k.c.do("PUT", "/api/desktop/display", map[string]int{"dpi": 20}, &bad); code != 400 || bad["code"] != "bad_dpi" {
		return fmt.Errorf("dpi 20: HTTP %d %v, want 400 bad_dpi", code, bad)
	}
	var ok map[string]any
	if code, err := k.c.do("PUT", "/api/desktop/display", map[string]int{"dpi": 144}, &ok); err != nil || code != 200 {
		return fmt.Errorf("dpi 144: HTTP %d %v %v", code, ok, err)
	}
	// Read it back from inside the session, with the session's own bus.
	script := `pid=$(pgrep -u dev -x xfce4-session | head -n 1)
[ -n "$pid" ] || { echo "no xfce4-session"; exit 1; }
for v in DISPLAY XAUTHORITY DBUS_SESSION_BUS_ADDRESS; do
  val=$(tr '\0' '\n' < /proc/$pid/environ | sed -n "s/^$v=//p")
  export "$v=$val"
done
xfconf-query -c xsettings -p /Xft/DPI`
	out, err := k.execUser("sh", "-c", script)
	if err != nil {
		return fmt.Errorf("xfconf-query: %v: %s", err, out)
	}
	if strings.TrimSpace(out) != "144" {
		return fmt.Errorf("xfconf reports %q", strings.TrimSpace(out))
	}
	st, err := k.status()
	if err != nil {
		return err
	}
	if st.DPI != 144 {
		return fmt.Errorf("GET /api/desktop dpi = %d", st.DPI)
	}
	return nil
}

func (k *checker) checkUsedTicket() error {
	t, err := k.c.ticket()
	if err != nil {
		return err
	}
	v, err := k.c.dial("ticket=" + t + "&width=1280&height=800")
	if err != nil {
		return err
	}
	v.CloseNow()
	code, reason, err := k.c.closeCodeFor("ticket=" + t)
	if err != nil {
		return err
	}
	if code != 4401 || !strings.HasPrefix(reason, "4401") {
		return fmt.Errorf("close %d %q", code, reason)
	}
	return nil
}

func (k *checker) checkExpiredTicket() error {
	t, err := k.c.ticket()
	if err != nil {
		return err
	}
	time.Sleep(desktop.TicketTTL + time.Second)
	code, reason, err := k.c.closeCodeFor("ticket=" + t)
	if err != nil {
		return err
	}
	if code != 4401 {
		return fmt.Errorf("close %d %q", code, reason)
	}
	return nil
}

func (k *checker) checkViewerCap(first *viewer) error {
	defer first.close()
	var open []*viewer
	defer func() {
		for _, v := range open {
			v.close()
		}
	}()
	// Viewers that earlier checks closed leave asynchronously; wait until
	// only the first is left so the count below is exact.
	for i := 0; i < 50; i++ {
		if st, err := k.status(); err == nil && st.Viewers == 1 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The first viewer is still open, so three more fill the cap.
	for i := 0; i < desktop.MaxViewers-1; i++ {
		v, err := k.c.openViewer(1280, 800, 96, "")
		if err != nil {
			return fmt.Errorf("viewer %d: %w", i+2, err)
		}
		open = append(open, v)
	}
	t, err := k.c.ticket()
	if err != nil {
		return err
	}
	code, reason, err := k.c.closeCodeFor("ticket=" + t + "&width=1280&height=800")
	if err != nil {
		return err
	}
	if code != 4429 {
		return fmt.Errorf("5th viewer: close %d %q", code, reason)
	}
	return nil
}

func (k *checker) checkListeners() error {
	out, err := k.exec("ss", "-ltnH")
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		local := f[3]
		for _, port := range []string{":3389", ":4822"} {
			if strings.HasSuffix(local, port) {
				if !strings.HasPrefix(local, "127.0.0.1:") {
					return fmt.Errorf("%s listens on %s", port, local)
				}
				found[port] = true
			}
		}
	}
	if !found[":3389"] || !found[":4822"] {
		return fmt.Errorf("listeners %v in:\n%s", found, out)
	}
	return nil
}

func (k *checker) checkAgentDisplay() error {
	k.exec("rm", "-f", "/tmp/helios-e2e-agent-display.txt")
	var created map[string]any
	code, err := k.c.do("POST", "/api/sessions", map[string]any{
		"provider": "claude", "prompt": "e2e: show me the display", "cwd": "/home/dev",
	}, &created)
	if err != nil || code >= 300 {
		return fmt.Errorf("create session: HTTP %d %v %v", code, created, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := k.exec("cat", "/tmp/helios-e2e-agent-display.txt")
		if err == nil {
			if !strings.Contains(out, "DISPLAY=:") || !strings.Contains(out, "name of display:") {
				return fmt.Errorf("agent saw:\n%s", out)
			}
			fmt.Printf("     %s\n", strings.ReplaceAll(strings.TrimSpace(out), "\n", "\n     "))
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("the agent never wrote its display report")
}

func (k *checker) exec(argv ...string) (string, error) {
	out, err := exec.Command("docker", append([]string{"exec", k.container}, argv...)...).CombinedOutput()
	return string(out), err
}

func (k *checker) execUser(argv ...string) (string, error) {
	out, err := exec.Command("docker", append([]string{"exec", "-u", "dev", k.container}, argv...)...).CombinedOutput()
	return string(out), err
}
