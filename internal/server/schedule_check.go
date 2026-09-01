package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

// How long a check may take, and how much of its output is kept.
//
// A check is not an agent and gets a shorter leash: it is a health probe, and
// one that hangs must read as a failure rather than as a reason to start an
// agent at 3am.
const (
	checkTimeout   = 30 * time.Second
	checkMaxOutput = 32 * 1024
)

// CheckResult is what a monitor's check saw.
type CheckResult struct {
	Exit   int
	Output string
	// Matched is whether this counts as "there is something to do".
	Matched bool
	// Failed is a check that could not be run or did not finish. Never a match:
	// a monitor that fires because its own probe broke is backwards.
	Failed bool
	Err    error
}

// RunCheck runs a monitor's check once and decides what it means.
//
// Two rules and no third:
//
//   - With a match pattern, the pattern over stdout decides and the exit code is
//     ignored. `grep ERROR app.log` exits 1 on a clean log, which is the good
//     case rather than a broken check.
//   - Without one, a non-zero exit is the match. This is the `test` convention:
//     the command asserts things are fine, and failing is the news.
func RunCheck(sc *store.Schedule) CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	cmd, err := checkCommand(ctx, sc)
	if err != nil {
		return CheckResult{Failed: true, Err: err}
	}
	cmd.Dir = sc.CWD
	// Its own process group, so a shell that spawns children does not leave
	// them running after the timeout kills the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	out, runErr := cmd.CombinedOutput()
	output := tailBytes(out, checkMaxOutput)

	// Kill the group, not just the child. Killing the shell alone leaves
	// whatever it started holding the terminal and the CPU.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	if ctx.Err() == context.DeadlineExceeded {
		return CheckResult{
			Exit: -1, Output: output, Failed: true,
			Err: fmt.Errorf("the check did not finish within %s", checkTimeout),
		}
	}

	exit := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return CheckResult{Output: output, Failed: true, Err: runErr}
		}
		exit = ee.ExitCode()
	}

	if sc.CheckMatch != "" {
		re, err := regexp.Compile(sc.CheckMatch)
		if err != nil {
			return CheckResult{Exit: exit, Output: output, Failed: true,
				Err: fmt.Errorf("match pattern: %w", err)}
		}
		return CheckResult{Exit: exit, Output: output, Matched: re.MatchString(output)}
	}
	return CheckResult{Exit: exit, Output: output, Matched: exit != 0}
}

// checkCommand builds the command a schedule's check describes, refusing a file
// that will not run before anything is started.
func checkCommand(ctx context.Context, sc *store.Schedule) (*exec.Cmd, error) {
	if sc.CheckFile != "" {
		path := expandHome(sc.CheckFile)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("check file %s: %w", sc.CheckFile, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("check file %s is a directory", sc.CheckFile)
		}
		if info.Mode()&0o111 == 0 {
			return nil, fmt.Errorf("check file %s is not executable — chmod +x it", sc.CheckFile)
		}
		// Run directly, by its own shebang, with no shell in between.
		return exec.CommandContext(ctx, path, sc.CheckArgs...), nil
	}
	if strings.TrimSpace(sc.CheckCmd) == "" {
		return nil, fmt.Errorf("this monitor has no check")
	}
	return exec.CommandContext(ctx, "sh", "-c", sc.CheckCmd), nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// tailBytes keeps the end of the output rather than the start: the interesting
// part of a failing build is the last screen, not the first.
func tailBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	cut := b[len(b)-max:]
	// Do not start mid-rune.
	for len(cut) > 0 && cut[0]&0xC0 == 0x80 {
		cut = cut[1:]
	}
	return "…\n" + string(cut)
}

// OutputPlaceholder is what a monitor's prompt writes to receive the check's
// output. A prompt without it simply does not get the output — nothing is
// appended behind the author's back.
const OutputPlaceholder = "{{output}}"

// FillPrompt substitutes the check's output into the prompt.
func FillPrompt(prompt, output string) string {
	return strings.ReplaceAll(prompt, OutputPlaceholder, output)
}
