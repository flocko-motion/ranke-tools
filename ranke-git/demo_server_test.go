// package: main / ranke-git
// type:    test
// job:     exercises demo-server against the real pinned ranke-db (server/run.sh, on its
// own PORT), not a fake — proving real find-or-build/content_hash reuse over the wire
// limits:  skips when run.sh/its binary aren't reachable at all
package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// freePort asks the OS for a currently-unused TCP port — this test's own
// instance, isolated from whatever a developer might already have running
// on run.sh's default :8080.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// startLiveServer runs server/run.sh, PORT-pinned to a freshly chosen port —
// the pinned release binary, installed on demand, against config.json: the
// same ephemeral, in-memory instance a developer gets by hand, just its own.
// The child carries Pdeathsig so the kernel reaps it even if this test
// process dies uncleanly, not only via the t.Cleanup below.
func startLiveServer(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	serverDir := filepath.Join(root, "server")
	script := filepath.Join(serverDir, "run.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("server/run.sh not found at %s: %v", script, err)
	}
	port := freePort(t)
	addr := "localhost:" + strconv.Itoa(port)

	cmd := exec.Command(script)
	cmd.Dir = serverDir
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	cmd.SysProcAttr = deathSigAttr()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server/run.sh: %v", err)
	}
	t.Cleanup(func() {
		stop := exec.Command(filepath.Join(serverDir, "stop.sh"))
		stop.Dir = serverDir
		stop.Env = cmd.Env
		_ = stop.Run()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		if cmd.ProcessState != nil {
			t.Fatalf("server/run.sh exited early; output:\n%s", out.String())
		}
		res, err := http.Get("http://" + addr + "/health")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return addr
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became healthy at %s; run.sh output:\n%s", addr, out.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// runDemoServerFor drives runDemoServer in-process against addr — no second
// binary to build — returning everything it printed.
func runDemoServerFor(t *testing.T, addr string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "x"}
	cmd.SetOut(&buf)
	cmd.SetContext(context.Background())
	o := &options{server: addr}
	if err := runDemoServer(cmd, o); err != nil {
		t.Fatalf("demo-server: %v\noutput so far:\n%s", err, buf.String())
	}
	return buf.String()
}

// TestDemoServerAgainstLiveServer runs demo-server twice, checking the two
// reuse mechanisms it exists to demonstrate: a cold first run finds nothing
// to reuse, a second finds its own entities and unchanged blobs and mints
// only the new commit.
func TestDemoServerAgainstLiveServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live-server test in -short mode")
	}
	addr := startLiveServer(t)

	first := runDemoServerFor(t, addr)
	if !strings.Contains(first, "0 known object(s) to reuse") {
		t.Errorf("first run: want a cold start, got:\n%s", first)
	}

	second := runDemoServerFor(t, addr)
	if strings.Contains(second, "0 known object(s) to reuse") {
		t.Errorf("second run: want reuse, got:\n%s", second)
	}
	if !strings.Contains(second, "merged 1 claim") {
		t.Errorf("second run: want only the new commit minted, got:\n%s", second)
	}
}
