package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRegistryCoversPlannedCommands(t *testing.T) {
	reg := Registry()
	want := []string{"setup", "migrate", "backup", "restore", "health", "status", "start", "stop", "logs"}
	for _, name := range want {
		cmd, ok := reg[name]
		if !ok {
			t.Errorf("Registry missing command %q", name)
			continue
		}
		if cmd.Run == nil {
			t.Errorf("command %q has nil Run", name)
		}
		if cmd.Summary == "" {
			t.Errorf("command %q has empty Summary", name)
		}
	}
}

func TestUsageListsServicesAndOperations(t *testing.T) {
	var buf bytes.Buffer
	Usage(&buf, []string{"serve", "worker"})
	out := buf.String()
	for _, want := range []string{"serve", "worker", "migrate", "backup", "operations:", "services:"} {
		if !strings.Contains(out, want) {
			t.Errorf("Usage output missing %q; got:\n%s", want, out)
		}
	}
}

func TestServerProcessStatus(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "test.pid")
	t.Setenv("LMS_PID_FILE", pidPath)

	// No pidfile → not running.
	if _, running := serverProcessStatus(); running {
		t.Error("expected not-running with no pidfile")
	}

	// Stale pid (very high, unlikely to exist) → not running, not a crash.
	if err := os.WriteFile(pidPath, []byte("999999999"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, running := serverProcessStatus(); running {
		t.Error("expected not-running for a stale pid")
	}

	// Our own pid → running.
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, running := serverProcessStatus()
	if !running || pid != os.Getpid() {
		t.Errorf("expected running with our pid %d, got pid=%d running=%v", os.Getpid(), pid, running)
	}

	// Garbage pidfile → not running.
	if err := os.WriteFile(pidPath, []byte("not-a-number"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, running := serverProcessStatus(); running {
		t.Error("expected not-running for a malformed pidfile")
	}
}

func TestRunLogsRejectsBadArgs(t *testing.T) {
	if err := runLogs([]string{"-n"}); err == nil {
		t.Error("expected error when -n has no value")
	}
	if err := runLogs([]string{"-n", "abc"}); err == nil {
		t.Error("expected error for non-numeric -n value")
	}
	if err := runLogs([]string{"--bogus"}); err == nil {
		t.Error("expected error for unknown argument")
	}
}

func TestPrintLastLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	content := "l1\nl2\nl3\nl4\nl5\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// printLastLines writes to stdout; we only assert it does not error and
	// leaves the offset at EOF (a follow would then read only new data).
	if err := printLastLines(f, 2); err != nil {
		t.Fatalf("printLastLines: %v", err)
	}
	pos, err := f.Seek(0, 1) // current offset
	if err != nil {
		t.Fatal(err)
	}
	if pos != int64(len(content)) {
		t.Errorf("expected offset at EOF (%d), got %d", len(content), pos)
	}
}
