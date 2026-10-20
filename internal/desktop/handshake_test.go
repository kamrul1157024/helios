package desktop

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
)

func TestClampSize(t *testing.T) {
	cases := []struct {
		w, h, dpi int
		want      Size
	}{
		{1280, 800, 96, Size{1280, 800, 96}},
		{10, 10, 0, Size{MinWidth, MinHeight, DefaultDPI}},
		{99999, 99999, 9999, Size{MaxWidth, MaxHeight, MaxDPI}},
		{1600, 900, 10, Size{1600, 900, MinDPI}},
	}
	for _, c := range cases {
		if got := ClampSize(c.w, c.h, c.dpi); got != c.want {
			t.Errorf("ClampSize(%d, %d, %d) = %+v, want %+v", c.w, c.h, c.dpi, got, c.want)
		}
	}
}

// guacdArgs is a cut-down args list in the shape guacd 1.6 sends: the version
// first, then names in its own order.
var guacdArgs = []string{"VERSION_1_5_0", "hostname", "port", "domain", "username", "password",
	"width", "height", "dpi", "security", "ignore-cert", "resize-method", "enable-wallpaper",
	"color-depth", "disable-audio", "client-name", "gateway-hostname", "drive-path"}

func TestConnectValuesAreFixedByTheDaemon(t *testing.T) {
	size := Size{Width: 1600, Height: 900, DPI: 144}
	got := connectValues(guacdArgs, size, Credentials{Username: "dev", Password: "s3cret"})
	want := map[string]string{
		"VERSION_1_5_0": protocolVersion, "hostname": "127.0.0.1", "port": "3389", "domain": "",
		"username": "dev", "password": "s3cret", "width": "1600", "height": "900", "dpi": "144",
		"security": "any", "ignore-cert": "true", "resize-method": "display-update",
		"enable-wallpaper": "false", "color-depth": "24", "disable-audio": "true",
		"client-name": "Helios", "gateway-hostname": "", "drive-path": "",
	}
	if len(got) != len(guacdArgs) {
		t.Fatalf("got %d values for %d names", len(got), len(guacdArgs))
	}
	for i, name := range guacdArgs {
		if got[i] != want[name] {
			t.Errorf("%s = %q, want %q", name, got[i], want[name])
		}
	}
}

// fakeGuacd plays guacd's half of the handshake and records what it got.
func fakeGuacd(t *testing.T, conn net.Conn, received chan<- []Instruction) {
	t.Helper()
	defer conn.Close()
	r := NewReader(conn)
	sel, err := r.Read()
	if err != nil || sel.Opcode != "select" {
		t.Errorf("first instruction = %+v, %v; want select", sel, err)
		return
	}
	io.WriteString(conn, Encode("args", guacdArgs...))
	got := []Instruction{sel}
	for {
		ins, err := r.Read()
		if err != nil {
			t.Errorf("read handshake: %v", err)
			return
		}
		got = append(got, ins)
		if ins.Opcode == "connect" {
			break
		}
	}
	io.WriteString(conn, Encode("ready", "$abc")+Encode("size", "0", "1600", "900"))
	received <- got
	io.Copy(io.Discard, conn)
}

func TestHandshakeSendsFixedTargetAndMatchingDPI(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan []Instruction, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		fakeGuacd(t, conn, received)
	}()

	gc, err := DialGuacd(context.Background(), ln.Addr().String(), ClampSize(1600, 900, 144),
		Credentials{Username: "dev", Password: "s3cret"}, "UTC")
	if err != nil {
		t.Fatalf("DialGuacd: %v", err)
	}
	defer gc.Close()
	if gc.ID != "$abc" {
		t.Errorf("ID = %q, want $abc", gc.ID)
	}

	got := <-received
	ops := []string{}
	for _, ins := range got {
		ops = append(ops, ins.Opcode)
	}
	if strings.Join(ops, ",") != "select,size,audio,video,image,timezone,connect" {
		t.Fatalf("handshake order = %v", ops)
	}
	if got[0].Args[0] != "rdp" {
		t.Errorf("select %v, want rdp", got[0].Args)
	}
	size, connect := got[1], got[6]
	if strings.Join(size.Args, "x") != "1600x900x144" {
		t.Errorf("size = %v", size.Args)
	}
	values := map[string]string{}
	for i, name := range guacdArgs {
		values[name] = connect.Args[i]
	}
	if values["dpi"] != size.Args[2] {
		t.Errorf("connect dpi %q != size dpi %q; the scale would not be 1", values["dpi"], size.Args[2])
	}
	if values["hostname"] != "127.0.0.1" || values["port"] != "3389" {
		t.Errorf("target = %s:%s", values["hostname"], values["port"])
	}

	// Bytes guacd sent after ready belong to the relay, not the handshake.
	next, err := NewReader(gc.Stream).Read()
	if err != nil || next.Opcode != "size" {
		t.Fatalf("first relayed instruction = %+v, %v", next, err)
	}
}

func TestHandshakeReportsGuacdError(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		r := NewReader(server)
		r.Read()
		io.WriteString(server, Encode("error", "No such protocol", "519"))
	}()
	_, err := handshake(client, ClampSize(1280, 800, 96), Credentials{}, "")
	if err == nil || !strings.Contains(err.Error(), "No such protocol") {
		t.Fatalf("handshake error = %v", err)
	}
}
