package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gitbackup.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadConfigFillsUnsetFlags pins that a config file supplies whatever the
// command line didn't.
func TestLoadConfigFillsUnsetFlags(t *testing.T) {
	path := writeConfig(t, "server: https://from-file.example.com\nbranch: from-file\n")
	var o options
	o.configPath = path
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().StringVar(&o.server, "server", "", "")
	cmd.Flags().StringVar(&o.branch, "branch", "main", "")
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := o.loadConfig(cmd); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if o.server != "https://from-file.example.com" {
		t.Errorf("server = %q, want the file's value", o.server)
	}
	if o.branch != "from-file" {
		t.Errorf("branch = %q, want the file's value", o.branch)
	}
}

// TestLoadConfigFlagsWinOverFile pins that a flag given on the command line
// is never overwritten by the config file, even when the flag's value equals
// its own default (branch's "main") — Changed, not the value, decides.
func TestLoadConfigFlagsWinOverFile(t *testing.T) {
	path := writeConfig(t, "server: https://from-file.example.com\nbranch: from-file\n")
	var o options
	o.configPath = path
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().StringVar(&o.server, "server", "", "")
	cmd.Flags().StringVar(&o.branch, "branch", "main", "")
	if err := cmd.ParseFlags([]string{"--server", "https://from-flag.example.com", "--branch", "main"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := o.loadConfig(cmd); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if o.server != "https://from-flag.example.com" {
		t.Errorf("server = %q, want the flag's value", o.server)
	}
	if o.branch != "main" {
		t.Errorf("branch = %q, want the flag's value (even though it equals the default)", o.branch)
	}
}

// TestLoadConfigNoPathIsANoop pins that an unset --config touches nothing —
// no flags registered at all here, so any write would be a nil-pointer panic,
// not just a wrong value.
func TestLoadConfigNoPathIsANoop(t *testing.T) {
	var o options
	o.branch = "untouched"
	if err := o.loadConfig(&cobra.Command{Use: "x"}); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if o.branch != "untouched" {
		t.Errorf("branch = %q, want it left alone", o.branch)
	}
}
