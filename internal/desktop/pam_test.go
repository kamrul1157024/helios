package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sesmanPAM = `#%PAM-1.0
auth       required   pam_env.so readenv=1
auth       required   pam_env.so readenv=1 envfile=/etc/default/locale
@include common-auth
@include common-account
@include common-session
@include common-password
`

func TestInsertPAMLineAboveCommonAuth(t *testing.T) {
	got := insertPAMLine(sesmanPAM)
	lines := strings.Split(got, "\n")
	if lines[3] != pamLine || lines[4] != "@include common-auth" {
		t.Fatalf("line not above common-auth:\n%s", got)
	}
	if again := insertPAMLine(got); again != got {
		t.Fatal("a second insert changed the file")
	}
	if back := removePAMLine(got); back != sesmanPAM {
		t.Fatalf("remove did not restore the file:\n%s", back)
	}
}

func TestInsertPAMLineWithoutInclude(t *testing.T) {
	in := "#%PAM-1.0\nauth required pam_unix.so\naccount required pam_unix.so\n"
	got := insertPAMLine(in)
	want := "#%PAM-1.0\n" + pamLine + "\nauth required pam_unix.so\naccount required pam_unix.so\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if got := insertPAMLine("#%PAM-1.0"); got != "#%PAM-1.0\n"+pamLine+"\n" {
		t.Fatalf("no auth rules: got %q", got)
	}
}

func TestHasPAMLineIgnoresSpacing(t *testing.T) {
	spaced := "auth   sufficient  pam_exec.so expose_authtok quiet  " + helperPath + "\n"
	if !hasPAMLine(spaced) {
		t.Fatal("did not recognise the line with other spacing")
	}
	if hasPAMLine("#" + pamLine) {
		t.Fatal("took a commented-out line for the real one")
	}
}

func TestHashFileContent(t *testing.T) {
	// sha256("abc"), as sha256sum prints it.
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n"
	if got := hashFileContent("abc"); got != want {
		t.Fatalf("got %q", got)
	}
}

// The helper runs under PAM as root; here it runs as the test user against a
// hash folder in a temp dir, which is the only difference.
func TestHelperScriptChecksTheSecret(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "desktop-auth")
	body := strings.ReplaceAll(helperScript, hashDir, dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev.sha256"), []byte(hashFileContent("s3cret")), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(user, password string) bool {
		cmd := exec.Command("/bin/sh", script)
		cmd.Env = []string{"PAM_USER=" + user}
		// pam_exec writes the password followed by a NUL byte.
		cmd.Stdin = strings.NewReader(password + "\x00")
		return cmd.Run() == nil
	}
	if !run("dev", "s3cret") {
		t.Error("the right secret failed")
	}
	if run("dev", "wrong") {
		t.Error("a wrong secret passed")
	}
	if run("other", "s3cret") {
		t.Error("a user with no hash file passed")
	}
	if run("../dev", "s3cret") {
		t.Error("a user name with a path passed")
	}
}
