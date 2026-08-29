package terminal

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// The registry signals a pid it recorded when the host started. Pids are
// recycled — on a machine running an agent per terminal, fast enough to turn
// the space over in an hour — so by the time a session is evicted that number
// can belong to anything, including the daemon. It died silently, and the
// registry believed it had stopped a host.
func TestIsHostProcessRejectsWhatIsNotTheHost(t *testing.T) {
	// This test binary is a live pid that is not a ptyhost: exactly the shape
	// of a recycled number.
	if IsHostProcess(os.Getpid(), "some-session") {
		t.Error("a process that is not a host was accepted")
	}
	if IsHostProcess(0, "some-session") || IsHostProcess(-1, "some-session") {
		t.Error("a pid that cannot exist was accepted")
	}
	// A pid nothing holds. 99998 is inside the range macOS recycles through,
	// so this asserts the check reads the process rather than the number.
	if IsHostProcess(99998, "some-session") && exec.Command("ps", "-p", "99998").Run() != nil {
		t.Error("a pid with no process was accepted")
	}
}

// The other half: a real ptyhost must be recognised, or eviction stops being
// able to stop anything and every closed session leaks its terminal.
func TestIsHostProcessAcceptsARealHost(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}
	// Not a real ptyhost — building one costs a compile — but a live process
	// whose command line carries what the check reads: the subcommand and the
	// session id. The sleep keeps it alive; the trailing words are what ps
	// shows.
	cmd := exec.Command(sh)
	// Two commands: with one, sh execs into it and the argv below is lost.
	cmd.Args = []string{"helios", "-c", "sleep 5; :", "ptyhost", "sess-owner-test"}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// ps reads the new command line only once the exec has landed.
	ok := false
	for i := 0; i < 50 && !ok; i++ {
		ok = IsHostProcess(cmd.Process.Pid, "sess-owner-test")
		if !ok {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !ok {
		t.Error("the session's own host was rejected, so it could never be stopped")
	}
	if IsHostProcess(cmd.Process.Pid, "a-different-session") {
		t.Error("another session's host was accepted")
	}
}

// Eviction with a recycled pid on record must not signal it. The victim here
// stands in for whatever the number belongs to now — on the machine this was
// found on, the candidates included the daemon.
func TestEvictLeavesARecycledPIDAlone(t *testing.T) {
	e := newRegistryEnv(t)
	e.add("sess-recycled", 0)

	bystander := exec.Command("sleep", "30")
	if err := bystander.Start(); err != nil {
		t.Fatalf("start bystander: %v", err)
	}
	// Waited on, so its death is observable: a signalled child that nobody
	// reaps stays a zombie, and a zombie still answers signal 0 as alive.
	exited := make(chan error, 1)
	go func() { exited <- bystander.Wait() }()
	defer func() {
		bystander.Process.Kill()
		<-exited
	}()

	// What a sidecar written hours ago looks like once the pid has come round
	// again: a live process that has nothing to do with this session.
	if err := WriteSidecar(e.dir, Sidecar{
		SessionID: "sess-recycled",
		PID:       bystander.Process.Pid,
		Cwd:       "/tmp",
	}); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	if err := e.reg.Evict("sess-recycled"); err != nil {
		t.Fatalf("evict: %v", err)
	}

	select {
	case err := <-exited:
		t.Fatalf("eviction killed a process that was not the host: %v", err)
	default:
	}
}
