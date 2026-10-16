package main

import (
	"os"
	"strings"
)

// tokenEnv names the environment variable holding the Partner API key.
const tokenEnv = "TALKSPIRIT_API_KEY"

// getToken returns the API key: $TALKSPIRIT_API_KEY when set, otherwise the
// platform credential store.
func getToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv(tokenEnv)); t != "" {
		return t, nil
	}
	return storedToken()
}
