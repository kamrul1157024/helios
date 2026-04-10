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

	fmt.Println(qr.ToSmallString(false))
	return nil
}
