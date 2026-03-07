//go:build !windows

package main

import (
	"fmt"
	"os"
)

func getToken() (string, error) {
	token := os.Getenv("HOLASPIRIT_TOKEN")
	if token == "" {
		return "", fmt.Errorf("HOLASPIRIT_TOKEN environment variable not set (Windows Credential Manager not available on this platform)")
	}
	return token, nil
}
