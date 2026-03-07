//go:build !windows

package main

import (
	"fmt"
	"os"
	"strings"
)

func getToken() (string, error) {
	token := strings.TrimSpace(os.Getenv("HOLASPIRIT_TOKEN"))
	if token == "" {
		return "", fmt.Errorf("HOLASPIRIT_TOKEN environment variable not set (Windows Credential Manager not available on this platform)")
	}
	return token, nil
}
