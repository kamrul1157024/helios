// Command probe drives the remote desktop e2e (test/e2e-desktop/run.sh).
//
//	probe pair  — inside the container: pair a device over the internal
//	              port, turn desktop.enabled on, write the key file.
//	probe check — on the host: walk the spec 66 checks against the daemon in
//	              the container, from "missing" to a live tunnel.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe pair|check [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "pair":
		err = runPair(os.Args[2:])
	case "check":
		err = runCheck(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
}

// keyFile is what pair writes and what the apps import: the device's id, its
// Ed25519 seed (base64url, as desktop/src/main/keys.ts keeps it), and the
// daemon URL as seen from the host.
type keyFile struct {
	KID  string `json:"kid"`
	Seed string `json:"seed"`
	URL  string `json:"url"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func runPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	internal := fs.String("internal", "http://127.0.0.1:7654", "internal API")
	public := fs.String("public", "http://127.0.0.1:7655", "public API, as reached from here")
	url := fs.String("url", "http://127.0.0.1:27655", "public API, as the host reaches it")
	out := fs.String("out", "/tmp/key.json", "where to write the key")
	fs.Parse(args)

	var created struct {
		Token string `json:"token"`
	}
	if err := postJSON(*internal+"/internal/device/create", map[string]any{}, &created); err != nil {
		return fmt.Errorf("device create: %w", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	kid := fmt.Sprintf("e2e-desktop-%d", time.Now().UnixNano())
	if err := postJSON(*public+"/api/auth/pair", map[string]any{
		"token": created.Token, "kid": kid, "public_key": b64(pub),
	}, nil); err != nil {
		return fmt.Errorf("pair: %w", err)
	}
	if err := postJSON(*internal+"/internal/device/activate", map[string]any{"kid": kid}, nil); err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	if err := putJSON(*internal+"/internal/settings", map[string]string{"desktop.enabled": "true"}); err != nil {
		return fmt.Errorf("enable desktop: %w", err)
	}
	raw, err := json.MarshalIndent(keyFile{KID: kid, Seed: b64(priv.Seed()), URL: *url}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("paired %s, desktop.enabled=true\n", kid)
	return nil
}

// client signs every request as the paired device.
type client struct {
	base string
	kid  string
	priv ed25519.PrivateKey
}

func loadClient(path string) (*client, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var k keyFile
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, err
	}
	seed, err := base64.RawURLEncoding.DecodeString(k.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("bad seed in %s", path)
	}
	return &client{base: strings.TrimRight(k.URL, "/"), kid: k.KID, priv: ed25519.NewKeyFromSeed(seed)}, nil
}

// token mirrors signJWT in desktop/src/main/keys.ts.
func (c *client) token() string {
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": c.kid})
	now := time.Now().Unix()
	payload, _ := json.Marshal(map[string]any{"iat": now, "exp": now + 3600, "sub": "helios-client"})
	input := b64(header) + "." + b64(payload)
	return input + "." + b64(ed25519.Sign(c.priv, []byte(input)))
}

// do sends a request and decodes a JSON answer into out when given.
func (c *client) do(method, path string, body, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s %s: %w: %s", method, path, err, data)
		}
	}
	return resp.StatusCode, nil
}

func postJSON(url string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func putJSON(url string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, data)
	}
	return nil
}
