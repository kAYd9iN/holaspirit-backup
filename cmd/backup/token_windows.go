//go:build windows

package main

import "github.com/kAYd9iN/holaspirit-backup/internal/credentials"

func getToken() (string, error) {
	return credentials.NewWinCredManager().GetToken()
}
