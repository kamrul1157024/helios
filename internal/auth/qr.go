package auth

import (
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

func PrintQR(payload string) error {
	qr, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return fmt.Errorf("generate qr: %w", err)
	}

	// true = include quiet zone border (required for reliable scanning)
	fmt.Println(qr.ToSmallString(true))
	return nil
}
