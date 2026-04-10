package tunnel

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

var defaultHTTPClient = &http.Client{Timeout: 5 * time.Second}

func getLANIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("get interface addrs: %w", err)
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no LAN IP found")
}
