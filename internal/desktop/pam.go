package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// D1: the daemon logs in to xrdp with a secret only it holds. PAM runs as
// root and must not trust a file the user can change, so root keeps only the
// secret's hash, and a short root-owned helper compares the two.

const (
	// helperPath is the PAM helper. Root-owned, so the user cannot swap the
	// check for one that passes everything.
	helperPath = "/usr/lib/helios/desktop-auth"
	// hashDir holds one <user>.sha256 for each user who installed the desktop.
	hashDir = "/etc/helios/desktop"
	// pamPath is the only PAM stack Helios touches. SSH, sudo and the console
	// login keep theirs.
	pamPath = "/etc/pam.d/xrdp-sesman"
)

// pamLine sends xrdp logins through the helper first. "sufficient" makes a
// match enough on its own, and a mismatch falls through to the normal
// password check, so a person with a password can still log in to xrdp.
const pamLine = "auth sufficient pam_exec.so expose_authtok quiet " + helperPath

// helperScript is the PAM helper. pam_exec hands it the password on stdin,
// followed by a NUL byte, and the user name in PAM_USER. It is POSIX sh so it
// runs on any distro without a build, and it reads at most 4 KiB so a hostile
// client cannot make it buffer without end.
const helperScript = `#!/bin/sh
# Installed by Helios (helios setup desktop). Lets the Helios daemon log in to
# xrdp with a secret only it holds: the password PAM passes on stdin must hash
# to the value in /etc/helios/desktop/<user>.sha256. Anything else fails here
# and falls through to the normal password check.
set -u
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

user=${PAM_USER:-}
case "$user" in
'' | */* | .*) exit 1 ;;
esac

want_file="/etc/helios/desktop/$user.sha256"
[ -f "$want_file" ] || exit 1
want=$(cut -d ' ' -f 1 "$want_file")
[ -n "$want" ] || exit 1

got=$(head -c 4096 | tr -d '\000' | sha256sum | cut -d ' ' -f 1)
[ "$got" = "$want" ]
`

// hashFileContent is what goes in /etc/helios/desktop/<user>.sha256.
func hashFileContent(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:]) + "\n"
}

// hasPAMLine reports whether the stack already sends logins to the helper.
func hasPAMLine(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.Join(strings.Fields(line), " ") == pamLine {
			return true
		}
	}
	return false
}

// insertPAMLine adds the helper line above "@include common-auth", where it
// runs before the password check. A stack without that include gets the line
// above its first auth rule, and one with no auth rules at the end. A stack
// that already has the line comes back unchanged.
func insertPAMLine(content string) string {
	if hasPAMLine(content) {
		return content
	}
	lines := strings.Split(content, "\n")
	at := -1
	for i, line := range lines {
		if strings.Join(strings.Fields(line), " ") == "@include common-auth" {
			at = i
			break
		}
	}
	if at < 0 {
		for i, line := range lines {
			if f := strings.Fields(line); len(f) > 0 && f[0] == "auth" {
				at = i
				break
			}
		}
	}
	if at < 0 {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + pamLine + "\n"
	}
	lines = append(lines[:at], append([]string{pamLine}, lines[at:]...)...)
	return strings.Join(lines, "\n")
}

// removePAMLine takes the helper line out again, and nothing else.
func removePAMLine(content string) string {
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.Join(strings.Fields(line), " ") == pamLine {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
