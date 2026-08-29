// keyprobe dials a session's ptyhost socket, optionally sends keys, and prints
// the rendered screen. It mirrors what backend.Host does, so what it observes
// is what the daemon would observe.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/kamrul1157024/helios/internal/terminal"
)

func main() {
	sock := os.Args[1]
	keys := os.Args[2:]

	c, err := terminal.Dial(sock, terminal.Hello{
		Role: terminal.RoleInteractive, Cols: 120, Rows: 40, Name: "keyprobe",
	})
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer c.Close()

	screen := terminal.NewScreen(120, 40)
	defer screen.Close()
	screen.StartDrain(nopWriter{})

	// Frames arrive on their own goroutine, because Next blocks and the probe
	// needs to send keys between reads.
	go func() {
		for {
			f, err := c.Next()
			if err != nil {
				return
			}
			switch f.Type {
			case terminal.FrameOutput:
				screen.Write(f.Payload)
			case terminal.FrameSnapshot:
				if _, ansi, err := terminal.DecodeSnapshot(f.Payload); err == nil {
					screen.Write(ansi)
				}
			}
		}
	}()
	pump := func(d time.Duration) { time.Sleep(d) }

	pump(3 * time.Second)
	for _, k := range keys {
		var b []byte
		switch k {
		case "enter":
			b = []byte("\r")
		case "down":
			b = []byte("\x1b[B")
		case "up":
			b = []byte("\x1b[A")
		case "esc":
			b = []byte("\x1b")
		default:
			b = []byte(k)
		}
		if err := c.Send(b); err != nil {
			fmt.Println("send:", err)
		}
		pump(2 * time.Second)
	}
	pump(2 * time.Second)
	fmt.Println(screen.Text())
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
