package credentials_test

import (
	"fmt"
	"testing"

	"github.com/kAYd9iN/holaspirit-backup/internal/credentials"
)

func TestMockReturnsToken(t *testing.T) {
	mock := &credentials.Mock{Token: "api:test123"}
	token, err := mock.GetToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "api:test123" {
		t.Errorf("got %q, want %q", token, "api:test123")
	}
}

func TestMockReturnsError(t *testing.T) {
	mock := &credentials.Mock{Err: fmt.Errorf("not found")}
	_, err := mock.GetToken()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestMockSatisfiesInterface(t *testing.T) {
	var _ credentials.Manager = &credentials.Mock{}
}
