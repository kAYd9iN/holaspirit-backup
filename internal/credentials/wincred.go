//go:build windows

package credentials

import (
	"fmt"

	"github.com/danieljoos/wincred"
)

const CredentialName = "holaspirit-backup"

// WinCredManager reads the API token from Windows Credential Manager.
type WinCredManager struct {
	name string
}

func NewWinCredManager() *WinCredManager {
	return &WinCredManager{name: CredentialName}
}

func (w *WinCredManager) GetToken() (string, error) {
	cred, err := wincred.GetGenericCredential(w.name)
	if err != nil {
		return "", fmt.Errorf("credential %q not found in Windows Credential Manager: %w", w.name, err)
	}
	return string(cred.CredentialBlob), nil
}
