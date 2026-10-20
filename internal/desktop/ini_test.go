package desktop

import "testing"

const xrdpINISample = `[Globals]
; xrdp.ini file version number
ini_version=1

; port=tcp://<any ipv4 format addr>:3389
port = 3389
#port=tcp://.:3389

use_vsock=false

[Logging]
LogFile=xrdp.log
`

func TestSetINIKeyReplacesInPlace(t *testing.T) {
	got := setINIKey(xrdpINISample, "Globals", "port", "tcp://127.0.0.1:3389")
	want := `[Globals]
; xrdp.ini file version number
ini_version=1

; port=tcp://<any ipv4 format addr>:3389
port=tcp://127.0.0.1:3389
#port=tcp://.:3389

use_vsock=false

[Logging]
LogFile=xrdp.log
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if again := setINIKey(got, "Globals", "port", "tcp://127.0.0.1:3389"); again != got {
		t.Fatal("setting the same value twice changed the file")
	}
}

func TestSetINIKeyAddsMissingKeyToSection(t *testing.T) {
	in := "[Sessions]\nX11DisplayOffset=10\n\n[Logging]\nLogFile=sesman.log\n"
	got := setINIKey(in, "Sessions", "Policy", "UB")
	want := "[Sessions]\nX11DisplayOffset=10\nPolicy=UB\n\n[Logging]\nLogFile=sesman.log\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetINIKeyAddsMissingSection(t *testing.T) {
	got := setINIKey("[Globals]\nport=3389", "Sessions", "Policy", "UB")
	want := "[Globals]\nport=3389\n[Sessions]\nPolicy=UB\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetINIKeyOnlyTouchesItsSection(t *testing.T) {
	in := "[A]\nPolicy=Default\n[Sessions]\nPolicy=Default\n"
	got := setINIKey(in, "Sessions", "Policy", "UB")
	if got != "[A]\nPolicy=Default\n[Sessions]\nPolicy=UB\n" {
		t.Fatalf("got %q", got)
	}
}

func TestINIValueAndRemove(t *testing.T) {
	if v, ok := iniValue(xrdpINISample, "Globals", "port"); !ok || v != "3389" {
		t.Fatalf("iniValue = %q, %v", v, ok)
	}
	if _, ok := iniValue(xrdpINISample, "Logging", "port"); ok {
		t.Fatal("found a key in the wrong section")
	}
	in := "[Sessions]\nPolicy=UB\nKillDisconnected=false\n"
	if got := removeINIKey(in, "Sessions", "Policy"); got != "[Sessions]\nKillDisconnected=false\n" {
		t.Fatalf("removeINIKey = %q", got)
	}
}
