package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pidFilePath and logFilePath are where `start` records the background
// server so `stop`, `status`, and `logs` can find it. Both are overridable
// for deployments that place runtime state elsewhere.
func pidFilePath() string {
	if p := os.Getenv("LMS_PID_FILE"); p != "" {
		return p
	}
	return "growth-lms.pid"
}

func logFilePath() string {
	if p := os.Getenv("LMS_LOG_FILE"); p != "" {
		return p
	}
	return "growth-lms.log"
}

// serverProcessStatus reads the pidfile and returns the pid and whether a
// live process actually holds it (signal 0 probes existence without
// affecting the target). A stale pidfile reports not-running.
func serverProcessStatus() (int, bool) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return pid, false
	}
	return pid, true
}

// runSetup bootstraps a fresh checkout: it creates .env from .env.example
// when missing (never overwriting an existing one), then applies all
// migrations so the developer has a ready-to-serve database.
func runSetup(args []string) error {
	if _, err := os.Stat(".env"); errors.Is(err, os.ErrNotExist) {
		example, err := os.ReadFile(".env.example")
		if err != nil {
			return fmt.Errorf("setup: read .env.example: %w", err)
		}
		if err := os.WriteFile(".env", example, 0o600); err != nil {
			return fmt.Errorf("setup: write .env: %w", err)
		}
		fmt.Println("created .env from .env.example — review and fill in secrets before starting")
	} else if err == nil {
		fmt.Println(".env already exists — leaving it untouched")
	} else {
		return fmt.Errorf("setup: stat .env: %w", err)
	}

	fmt.Println("applying migrations ...")
	if err := runMigrate([]string{"up"}); err != nil {
		return err
	}
	fmt.Println("setup complete")
	return nil
}

// runStart launches `app serve` as a detached background process, writing
// its pid to the pidfile and redirecting output to the log file. It refuses
// to start a second server if one is already running.
func runStart(args []string) error {
	if pid, running := serverProcessStatus(); running {
		return fmt.Errorf("server already running (pid %d)", pid)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("start: locate executable: %w", err)
	}
	logFile, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("start: open log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(self, "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Detach into its own session so it survives the parent shell exiting.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: launch serve: %w", err)
	}

	if err := os.WriteFile(pidFilePath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("start: write pidfile: %w", err)
	}
	// Release the child so it is not reaped when this short-lived CLI exits.
	_ = cmd.Process.Release()
	fmt.Printf("server started (pid %d), logging to %s\n", cmd.Process.Pid, logFilePath())
	return nil
}

// runStop sends SIGTERM to the background server and waits briefly for it to
// exit, then removes the pidfile. It escalates to SIGKILL only if the
// process ignores the graceful signal.
func runStop(args []string) error {
	pid, running := serverProcessStatus()
	if !running {
		_ = os.Remove(pidFilePath())
		return errors.New("server is not running")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("stop: find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop: signal process %d: %w", pid, err)
	}

	for i := 0; i < 50; i++ {
		if _, stillRunning := serverProcessStatus(); !stillRunning {
			_ = os.Remove(pidFilePath())
			fmt.Printf("server (pid %d) stopped\n", pid)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(pidFilePath())
	fmt.Printf("server (pid %d) did not stop gracefully; sent SIGKILL\n", pid)
	return nil
}

// runLogs prints the background server log. With -f it follows the file,
// polling for appended data until interrupted; -n limits the initial tail.
func runLogs(args []string) error {
	follow := false
	lines := 50
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--follow":
			follow = true
		case "-n":
			if i+1 >= len(args) {
				return errors.New("logs: -n requires a line count")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("logs: invalid -n value %q", args[i+1])
			}
			lines = n
			i++
		default:
			return fmt.Errorf("logs: unknown argument %q", args[i])
		}
	}

	f, err := os.Open(logFilePath())
	if err != nil {
		return fmt.Errorf("logs: open %s: %w", logFilePath(), err)
	}
	defer f.Close()

	if err := printLastLines(f, lines); err != nil {
		return err
	}
	if !follow {
		return nil
	}
	return followFile(f)
}

// printLastLines reads f fully and prints its final n lines, then leaves the
// offset at end-of-file for a subsequent follow.
func printLastLines(f *os.File, n int) error {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var buf []string
	for scanner.Scan() {
		buf = append(buf, scanner.Text())
		if n > 0 && len(buf) > n {
			buf = buf[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("logs: read: %w", err)
	}
	for _, line := range buf {
		fmt.Println(line)
	}
	return nil
}

// followFile polls for data appended after the current offset and prints it.
// It runs until the process is interrupted (Ctrl-C), matching `tail -f`.
func followFile(f *os.File) error {
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			fmt.Print(line)
			continue
		}
		if err != nil && err != io.EOF {
			return fmt.Errorf("logs: follow: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
