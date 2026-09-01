package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/daemon"
)

// `helios schedule` — the surface every other one is built on.
//
// Not a convenience: the desktop's "describe what you want" box works by
// running an agent that reads this command's help and calls it, so `add` has to
// express everything a schedule can be and the usage below has to be good
// enough to get right on one reading.

const scheduleUsage = `Usage: helios schedule <command>

  list                       every schedule, with what it does and when
  add "<prompt>" [flags]     create one
  edit <name> [flags]        change one
  rm <name>                  delete it, and detach whatever followed it
  enable <name>              unpause
  disable <name>             pause, keeping its place in the clock
  run <name>                 fire it now, out of turn
  check <name>               run a monitor's check once, without firing
  logs <name> [--follow]     what its checks and fires have printed

Flags for add and edit:

  --name <name>              what it is called; required for add
  --cron "<expr>"            five fields, local time: "0 9 * * 1-5"
  --at <RFC3339>             run once, at one moment, then be done
  --after <name>             run when that job finishes, instead of on a clock
  --after-when success|any   whether a failed parent still counts (default success)
  --cwd <dir>                where it runs; omit for work that is not about a directory
  --provider <id>            claude, codex … (default: the daemon's own default)
  --model <model>
  --permission-mode <mode>
  --resume <session-id>      send the prompt into that session instead of a new one

  --check "<command>"        make it a monitor: run this, fire when it matters
  --check-file <path>        or run this script directly, by its shebang
  --check-arg <arg>          an argument for that script; repeatable
  --match "<regexp>"         fire when the output matches this, whatever the exit code

A monitor without --match fires when its check exits non-zero, which is the
'test' convention: the command asserts things are fine and failing is the news.
With --match the pattern decides and the exit code is ignored, because a grep
that finds nothing exits 1 and that is the good case.

A monitor's prompt may contain {{output}}, which is replaced with what the
check printed.

Examples:

  helios schedule add "triage the overnight PRs" \
    --name morning-triage --cron "0 9 * * 1-5" --cwd ~/work/app

  helios schedule add "The tests are failing:\n\n{{output}}\n\nFix them." \
    --name build-watch --cron "*/15 * * * *" --cwd ~/work/app \
    --check "make test 2>&1"

  helios schedule add "Review the PRs waiting on me:\n\n{{output}}" \
    --name pr-review --cron "0 */2 * * 1-5" \
    --check "gh pr list --search 'review-requested:@me' --json number,title" \
    --match '"number"'

  helios schedule add "run the feature-two tests" --name test-two \
    --after nightly-migrate --after-when success --cwd ~/work/app
`

// wireSchedule is a schedule as the API reports it.
type wireSchedule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Enabled       bool   `json:"enabled"`
	Cron          string `json:"cron"`
	RunAt         string `json:"run_at"`
	AfterID       string `json:"after_id"`
	AfterWhen     string `json:"after_when"`
	Mode          string `json:"mode"`
	Prompt        string `json:"prompt"`
	CWD           string `json:"cwd"`
	CheckCmd      string `json:"check_cmd"`
	CheckFile     string `json:"check_file"`
	CheckMatch    string `json:"check_match"`
	TargetSession string `json:"target_session"`
	NextRunAt     string `json:"next_run_at"`
	LastFiredAt   string `json:"last_fired_at"`
	LastStatus    string `json:"last_status"`
	LastError     string `json:"last_error"`
	DoneAt        string `json:"done_at"`
	FailStreak    int    `json:"fail_streak"`
	FiresToday    int    `json:"fires_today"`
}

func handleSchedule(args []string) {
	if len(args) == 0 {
		fmt.Print(scheduleUsage)
		return
	}

	switch args[0] {
	case "list", "ls":
		scheduleList()
	case "add", "new":
		scheduleAdd(args[1:])
	case "edit":
		scheduleEdit(args[1:])
	case "rm", "delete":
		scheduleSimple(args[1:], http.MethodDelete, "", "deleted")
	case "enable":
		scheduleEnable(args[1:], true)
	case "disable":
		scheduleEnable(args[1:], false)
	case "run":
		scheduleSimple(args[1:], http.MethodPost, "/run", "fired")
	case "check":
		scheduleCheck(args[1:])
	case "logs", "log":
		scheduleLogs(args[1:])
	case "help", "--help", "-h":
		fmt.Print(scheduleUsage)
	default:
		fmt.Fprintf(os.Stderr, "Unknown: helios schedule %s\n\n", args[0])
		fmt.Fprint(os.Stderr, scheduleUsage)
		os.Exit(1)
	}
}

func scheduleURL(path string) string {
	cfg, _ := daemon.LoadConfig()
	return fmt.Sprintf("http://127.0.0.1:%d/internal/schedules%s", cfg.Server.InternalPort, path)
}

// callSchedules talks to the daemon and reports what it said, so no caller has
// to unwrap an error body twice.
func callSchedules(method, path string, body interface{}) (map[string]interface{}, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, scheduleURL(path), reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon not running? %w", err)
	}
	defer resp.Body.Close()

	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 400 {
		msg, _ := out["message"].(string)
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return out, nil
}

func fetchSchedules() ([]wireSchedule, error) {
	out, err := callSchedules(http.MethodGet, "", nil)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(out["schedules"])
	var schedules []wireSchedule
	json.Unmarshal(raw, &schedules)
	return schedules, nil
}

// findSchedule resolves the name a person typed to the schedule it names.
func findSchedule(name string) (*wireSchedule, error) {
	schedules, err := fetchSchedules()
	if err != nil {
		return nil, err
	}
	for i := range schedules {
		if schedules[i].Name == name || schedules[i].ID == name {
			return &schedules[i], nil
		}
	}
	return nil, fmt.Errorf("no schedule called %q", name)
}

func scheduleList() {
	schedules, err := fetchSchedules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(schedules) == 0 {
		fmt.Println("No schedules yet. Try: helios schedule add \"…\" --name nightly --cron \"0 2 * * *\"")
		return
	}

	parentOf := map[string]string{}
	for _, sc := range schedules {
		parentOf[sc.ID] = sc.AfterID
	}
	depthOf := func(sc *wireSchedule) int {
		depth := 0
		for at := sc.AfterID; at != "" && depth < 16; at = parentOf[at] {
			depth++
		}
		return depth
	}

	fmt.Printf("%-22s %-18s %-18s %-10s %s\n", "NAME", "WHEN", "NEXT/CHECK", "LAST", "DOES")
	fmt.Println(strings.Repeat("-", 100))
	for _, sc := range schedules {
		// Indented under its parent, which the daemon has already ordered them
		// for. The depth is the length of the chain above it, so a grandchild
		// does not read as a sibling.
		name := sc.Name
		if depth := depthOf(&sc); depth > 0 {
			name = strings.Repeat("   ", depth-1) + "└─ " + name
		}
		fmt.Printf("%-22s %-18s %-18s %-10s %s\n",
			truncateCol(name, 22), whenColumn(&sc), nextColumn(&sc),
			lastColumn(&sc), doesColumn(&sc))
	}

	var paused, missed, done int
	for _, sc := range schedules {
		switch {
		case sc.DoneAt != "":
			// A one-shot that has had its moment is finished, not paused.
			done++
		case !sc.Enabled:
			paused++
		}
		if sc.LastStatus == "missed" {
			missed++
		}
	}
	summary := fmt.Sprintf("\n%d schedule%s", len(schedules), plural(len(schedules)))
	if paused > 0 {
		summary += fmt.Sprintf(" · %d paused", paused)
	}
	if done > 0 {
		summary += fmt.Sprintf(" · %d done", done)
	}
	if missed > 0 {
		summary += fmt.Sprintf(" · %d missed", missed)
	}
	fmt.Println(summary)
}

func whenColumn(sc *wireSchedule) string {
	switch sc.Kind {
	case "once":
		return "once"
	case "after":
		when := "✓only"
		if sc.AfterWhen == "any" {
			when = "either"
		}
		return "after ↑ " + when
	default:
		return truncateCol(sc.Cron, 18)
	}
}

func nextColumn(sc *wireSchedule) string {
	switch {
	case sc.DoneAt != "":
		return "done"
	case !sc.Enabled:
		return "paused"
	case sc.Kind == "after":
		return "waiting"
	case sc.NextRunAt == "":
		return "—"
	}
	next, err := time.Parse(time.RFC3339, sc.NextRunAt)
	if err != nil {
		return sc.NextRunAt
	}
	return whenText(next)
}

// whenText is how far off something is, in words a person reads at a glance.
// humanDuration answers "how long ago", which reads wrong in the other
// direction: a run due in forty seconds is not "just now".
func whenText(at time.Time) string {
	local := at.Local()
	until := time.Until(local)
	switch {
	case until < 0:
		return "due"
	case until < time.Minute:
		return fmt.Sprintf("in %ds", int(until.Seconds()))
	case until < time.Hour:
		return fmt.Sprintf("in %dm", int(until.Minutes()))
	case until < 12*time.Hour:
		return fmt.Sprintf("in %dh %dm", int(until.Hours()), int(until.Minutes())%60)
	case local.YearDay() == time.Now().YearDay() && local.Year() == time.Now().Year():
		return "today " + local.Format("15:04")
	default:
		return local.Format("Mon 15:04")
	}
}

func lastColumn(sc *wireSchedule) string {
	switch sc.LastStatus {
	case "":
		return "—"
	case "running":
		return "● running"
	case "missed":
		return "! missed"
	case "blocked":
		return "⊘ blocked"
	case "failed":
		if sc.FailStreak > 1 {
			return fmt.Sprintf("✗ ×%d", sc.FailStreak)
		}
		return "✗ failed"
	}
	if sc.LastFiredAt != "" {
		if at, err := time.Parse(time.RFC3339, sc.LastFiredAt); err == nil {
			return "✓ " + humanDuration(time.Since(at))
		}
	}
	return "✓"
}

func doesColumn(sc *wireSchedule) string {
	what := "new"
	if sc.Mode == "resume" {
		what = "resume · " + shortID(sc.TargetSession)
	} else if sc.CWD != "" {
		what = "new · " + sc.CWD
	}
	if sc.Kind == "monitor" {
		check := sc.CheckCmd
		if check == "" {
			check = sc.CheckFile
		}
		return "monitor · " + truncateCol(check, 40)
	}
	return what
}

// ── add and edit ────────────────────────────────────────────────────────────

// scheduleFlags is the shared flag reader for add and edit. Only what was
// actually typed is sent, so an edit does not clear what it did not mention.
func scheduleFlags(args []string) (map[string]interface{}, error) {
	body := map[string]interface{}{}
	var checkArgs []string
	sawCheckArg := false

	for i := 0; i < len(args); i++ {
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", args[i])
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch args[i] {
		case "--name":
			v, err = next()
			body["name"] = v
		case "--cron":
			v, err = next()
			body["cron"] = v
			body["kind"] = "timer"
		case "--at":
			v, err = next()
			body["run_at"] = v
			body["kind"] = "once"
		case "--after":
			v, err = next()
			body["after_name"] = v
			body["kind"] = "after"
		case "--after-when":
			v, err = next()
			body["after_when"] = v
		case "--cwd":
			v, err = next()
			body["cwd"] = v
		case "--provider":
			v, err = next()
			body["provider"] = v
		case "--model":
			v, err = next()
			body["model"] = v
		case "--permission-mode":
			v, err = next()
			body["permission_mode"] = v
		case "--resume":
			v, err = next()
			body["target_session"] = v
			body["mode"] = "resume"
		case "--check":
			v, err = next()
			body["check_cmd"] = v
			body["kind"] = "monitor"
		case "--check-file":
			v, err = next()
			body["check_file"] = v
			body["kind"] = "monitor"
		case "--check-arg":
			v, err = next()
			checkArgs = append(checkArgs, v)
			sawCheckArg = true
		case "--match":
			v, err = next()
			body["check_match"] = v
		default:
			return nil, fmt.Errorf("unknown flag %q", args[i])
		}
		if err != nil {
			return nil, err
		}
	}
	if sawCheckArg {
		body["check_args"] = checkArgs
	}
	return body, nil
}

// resolveAfter turns --after <name> into the id the API takes.
func resolveAfter(body map[string]interface{}) error {
	name, ok := body["after_name"].(string)
	if !ok {
		return nil
	}
	delete(body, "after_name")
	parent, err := findSchedule(name)
	if err != nil {
		return err
	}
	body["after_id"] = parent.ID
	return nil
}

func scheduleAdd(args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, scheduleUsage)
		os.Exit(1)
	}
	body, err := scheduleFlags(args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	// Escapes are what a person types in a shell single-quoted string, and a
	// prompt with a literal \n in it reads badly to an agent.
	body["prompt"] = strings.ReplaceAll(args[0], `\n`, "\n")
	if err := resolveAfter(body); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	out, err := callSchedules(http.MethodPost, "", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	printSaved(out)
}

func scheduleEdit(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios schedule edit <name> [flags]")
		os.Exit(1)
	}
	sc, err := findSchedule(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	body, err := scheduleFlags(args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := resolveAfter(body); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	out, err := callSchedules(http.MethodPatch, "/"+sc.ID, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	printSaved(out)
}

// printSaved reads back what was understood, which is the whole validation
// story: the expression, what it means, and when it next fires.
func printSaved(out map[string]interface{}) {
	raw, _ := json.Marshal(out["schedule"])
	var sc wireSchedule
	json.Unmarshal(raw, &sc)

	fmt.Printf("%s — %s\n", sc.Name, describeSchedule(&sc))
	if sc.NextRunAt != "" {
		if next, err := time.Parse(time.RFC3339, sc.NextRunAt); err == nil {
			word := "first run"
			if sc.Kind == "monitor" {
				word = "first check"
			}
			fmt.Printf("%*s%s %s (%s)\n", len(sc.Name)+3, "", word,
				next.Local().Format("Mon 2 Jan 15:04"), whenText(next))
		}
	}
}

func describeSchedule(sc *wireSchedule) string {
	switch sc.Kind {
	case "monitor":
		check := sc.CheckCmd
		if check == "" {
			check = sc.CheckFile
		}
		if sc.CheckMatch != "" {
			return fmt.Sprintf("checks %s, fires when `%s` output matches", sc.Cron, check)
		}
		return fmt.Sprintf("checks %s, fires when `%s` exits non-zero", sc.Cron, check)
	case "once":
		return "runs once, at " + sc.RunAt
	case "after":
		when := "only if it succeeds"
		if sc.AfterWhen == "any" {
			when = "either way"
		}
		return fmt.Sprintf("runs after another job, %s", when)
	default:
		return "cron " + sc.Cron
	}
}

// ── the small ones ──────────────────────────────────────────────────────────

func scheduleSimple(args []string, method, suffix, done string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Which schedule? Try: helios schedule list")
		os.Exit(1)
	}
	sc, err := findSchedule(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if _, err := callSchedules(method, "/"+sc.ID+suffix, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s %s\n", sc.Name, done)
}

func scheduleEnable(args []string, enabled bool) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Which schedule? Try: helios schedule list")
		os.Exit(1)
	}
	sc, err := findSchedule(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	out, err := callSchedules(http.MethodPatch, "/"+sc.ID, map[string]interface{}{"enabled": enabled})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	word := "paused"
	if enabled {
		word = "enabled"
	}
	fmt.Printf("%s %s\n", sc.Name, word)
	if enabled {
		printSaved(out)
	}
}

func scheduleCheck(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Which monitor? Try: helios schedule list")
		os.Exit(1)
	}
	sc, err := findSchedule(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	out, err := callSchedules(http.MethodPost, "/"+sc.ID+"/check", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	exit, _ := out["exit"].(float64)
	matched, _ := out["matched"].(bool)
	failed, _ := out["failed"].(bool)
	output, _ := out["output"].(string)

	switch {
	case failed:
		fmt.Printf("check failed — %v\n", out["error"])
	case matched:
		fmt.Printf("exit %d — MATCH, this would fire\n", int(exit))
	default:
		fmt.Printf("exit %d — quiet, this would not fire\n", int(exit))
	}
	if strings.TrimSpace(output) != "" {
		fmt.Println("---")
		fmt.Println(strings.TrimRight(output, "\n"))
	}
}

// scheduleLogs prints the tail, and follows it by asking again.
//
// Polling rather than streaming: there is no streaming log anywhere in the
// daemon, and a check that runs every five minutes does not need sub-second
// delivery.
func scheduleLogs(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Which schedule? Try: helios schedule list")
		os.Exit(1)
	}
	sc, err := findSchedule(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	follow := false
	for _, a := range args[1:] {
		if a == "--follow" || a == "-f" {
			follow = true
		}
	}

	seen := map[string]bool{}
	print := func(first bool) {
		out, err := callSchedules(http.MethodGet, "/"+sc.ID+"/log?tail=200", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		lines, _ := out["lines"].([]interface{})
		for _, l := range lines {
			line, _ := l.(string)
			if seen[line] {
				continue
			}
			seen[line] = true
			fmt.Println(line)
		}
		if first && len(lines) == 0 {
			fmt.Println("(nothing yet)")
		}
	}

	print(true)
	if !follow {
		return
	}
	for {
		time.Sleep(2 * time.Second)
		print(false)
	}
}

// ── small helpers ───────────────────────────────────────────────────────────

func truncateCol(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
