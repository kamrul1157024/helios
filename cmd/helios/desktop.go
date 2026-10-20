package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/desktop"
	"github.com/kamrul1157024/helios/internal/version"
)

// setupDesktop runs the remote desktop install from a terminal, where sudo
// can ask for a password. It is the same six steps the daemon runs through
// `sudo -n`, for the machine where that fails (docs/specs/66-remote-desktop.md,
// D4). The daemon's half of the last step — guacd and the warm-up — is left to
// the daemon, which starts both the next time anyone asks for the desktop.
func setupDesktop(args []string) {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "Remote desktop needs a Linux host.")
		os.Exit(1)
	}
	uninstall := len(args) > 0 && args[0] == "--uninstall"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	inst := desktop.NewInstaller(desktop.NewHostSystem(true), daemon.HeliosDir(), version.Current)
	if uninstall {
		left, err := inst.Uninstall(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Removed the Helios login for xrdp, the xrdp settings Helios changed, and the desktop service.")
		fmt.Printf("Left in place: the packages %s, and ~/.xsession.\n", strings.Join(left, ", "))
		fmt.Printf("Remove the packages with: sudo apt-get remove %s\n", strings.Join(left, " "))
		return
	}

	printed := map[string]desktop.StepState{}
	inst.Progress = func(steps []desktop.Step) {
		for _, s := range steps {
			if printed[s.ID] == s.State {
				continue
			}
			printed[s.ID] = s.State
			switch s.State {
			case desktop.StepRunning:
				fmt.Printf("… %s\n", s.Label)
			case desktop.StepDone:
				fmt.Printf("✓ %s\n", s.Label)
			case desktop.StepSkipped:
				fmt.Printf("- %s (not needed)\n", s.Label)
			case desktop.StepFailed:
				fmt.Printf("✗ %s: %s\n", s.Label, s.Line)
			}
		}
	}
	if err := inst.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nThe desktop is installed. Open it from the Helios app; the daemon starts it on first use.")
	if err := enableDesktop(); err != nil {
		fmt.Printf("Could not turn on the setting desktop.enabled (%v). Start the daemon and run this again.\n", err)
	}
}

// enableDesktop turns on desktop.enabled. Running the install is as clear a
// request for the desktop as there is, so the setting that hides it for this
// release should not need a second trip.
func enableDesktop() error {
	cfg, err := daemon.LoadConfig()
	if err != nil {
		return err
	}
	body := strings.NewReader(`{"` + desktop.SettingEnabled + `":"true"}`)
	url := fmt.Sprintf("http://127.0.0.1:%d/internal/settings", cfg.Server.InternalPort)
	req, err := http.NewRequest(http.MethodPut, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("daemon not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("daemon answered %s", resp.Status)
	}
	return nil
}
