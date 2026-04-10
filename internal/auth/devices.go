package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

func InitDevice(name string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	count, err := db.CountDevices()
	if err != nil {
		return fmt.Errorf("count devices: %w", err)
	}

	kid := fmt.Sprintf("device-%03d", count+1)

	kp, err := GenerateKeypair(kid)
	if err != nil {
		return err
	}

	device := &store.Device{
		KID:       kid,
		Name:      name,
		PublicKey: kp.PublicKeyBase64(),
		Status:    "active",
	}

	if err := db.CreateDevice(device); err != nil {
		return fmt.Errorf("store device: %w", err)
	}

	payload := kp.SetupPayload()

	fmt.Println()
	fmt.Println("  Helios Device Setup")
	fmt.Println("  -------------------")
	fmt.Println()
	fmt.Println("  Scan this QR code with your browser:")
	fmt.Println()

	if err := PrintQR(payload); err != nil {
		fmt.Printf("  (QR generation failed: %v)\n", err)
	}

	fmt.Println()
	fmt.Println("  Or copy this setup string:")
	fmt.Printf("  %s\n", payload)
	fmt.Println()
	fmt.Printf("  Key ID: %s\n", kid)
	fmt.Printf("  Name:   %s\n", name)
	fmt.Println()

	return nil
}

func ListDevices() error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	devices, err := db.ListDevices()
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}

	if len(devices) == 0 {
		fmt.Println("No devices registered. Run: helios auth init --name \"My Device\"")
		return nil
	}

	fmt.Printf("%-14s %-20s %-10s %s\n", "Key ID", "Name", "Status", "Last Seen")
	fmt.Println(strings.Repeat("-", 60))

	for _, d := range devices {
		lastSeen := "never"
		if d.LastSeenAt != nil {
			t, err := time.Parse(time.RFC3339, *d.LastSeenAt)
			if err == nil {
				lastSeen = humanDuration(time.Since(t))
			}
		}
		fmt.Printf("%-14s %-20s %-10s %s\n", d.KID, d.Name, d.Status, lastSeen)
	}

	return nil
}

func RevokeDevice(kid string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	device, err := db.GetDevice(kid)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}
	if device == nil {
		return fmt.Errorf("device %q not found", kid)
	}

	if err := db.RevokeDevice(kid); err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}

	fmt.Printf("Device %q (%s) revoked\n", kid, device.Name)
	return nil
}

func openDB() (*store.Store, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".helios", "helios.db")
	return store.Open(dbPath)
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
