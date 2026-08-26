package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestRunIdentityRegisterRefusesExistingFile pins the safety guard: a
// signing key already at --out is never overwritten, checked before any
// network call (no server needed for this test).
func TestRunIdentityRegisterRefusesExistingFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(out, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing key: %v", err)
	}
	o := &options{server: "localhost:0"}
	err := runIdentityRegister(&cobra.Command{}, o, out)
	if err == nil {
		t.Fatal("runIdentityRegister: want an error for an existing --out, got nil")
	}
	content, readErr := os.ReadFile(out)
	if readErr != nil || string(content) != "existing" {
		t.Errorf("existing key file was touched: %q, %v", content, readErr)
	}
}
