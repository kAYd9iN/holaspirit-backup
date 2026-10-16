//go:build windows

package main

import "github.com/kAYd9iN/holaspirit-backup/internal/credentials"

// storedToken reads the key from Windows Credential Manager.
func storedToken() (string, error) {
	return credentials.NewWinCredManager().GetToken()
}
