//go:build !windows

package main

import "fmt"

// storedToken is the platform credential store fallback; there is none here.
func storedToken() (string, error) {
	return "", fmt.Errorf("%s environment variable not set (Windows Credential Manager not available on this platform)", tokenEnv)
}
