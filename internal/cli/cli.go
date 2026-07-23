// Package cli implements the operational subcommands of the single Growth
// LMS binary — the commands an operator runs by hand or from a deployment
// script rather than the long-running services. `serve` and `worker` live
// in cmd/app because they own their own dependency wiring and process
// lifetime; everything else (setup, migrate, backup, restore, health,
// status, start, stop, logs) is a short-lived operational action and lives
// here so cmd/app stays small.
//
// Every command here reads the same validated config.Config the services
// use, so an operator can never point a backup at one database while the
// server talks to another.
package cli

import (
	"fmt"
	"io"
	"sort"
)

// Command is one operational subcommand.
type Command struct {
	Name    string
	Summary string
	// Run receives the arguments that follow the subcommand name. It
	// returns an error to signal a non-zero exit; the dispatcher prints it.
	Run func(args []string) error
}

// Registry is the set of operational commands, keyed by name.
func Registry() map[string]Command {
	cmds := []Command{
		{Name: "setup", Summary: "Create .env from .env.example (if missing) and run all migrations", Run: runSetup},
		{Name: "migrate", Summary: "Apply/revert schema migrations: migrate <up|down [n]|version|force <v>>", Run: runMigrate},
		{Name: "health", Summary: "Check Postgres and Redis connectivity, exit non-zero if unhealthy", Run: runHealth},
		{Name: "status", Summary: "Report whether the server is running (pidfile) and dependency health", Run: runStatus},
		{Name: "backup", Summary: "Dump the database with pg_dump: backup [output-path]", Run: runBackup},
		{Name: "restore", Summary: "Restore a database dump with pg_restore: restore <input-path>", Run: runRestore},
		{Name: "start", Summary: "Launch `serve` as a detached background process with a pidfile and log", Run: runStart},
		{Name: "stop", Summary: "Stop the background server started by `start` (SIGTERM via pidfile)", Run: runStop},
		{Name: "logs", Summary: "Print the background server log: logs [-f] [-n lines]", Run: runLogs},
	}
	out := make(map[string]Command, len(cmds))
	for _, c := range cmds {
		out[c.Name] = c
	}
	return out
}

// Usage writes the list of operational commands (plus the service commands
// owned by cmd/app) to w.
func Usage(w io.Writer, serviceCommands []string) {
	fmt.Fprintln(w, "usage: app <command> [args]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "services:")
	for _, name := range serviceCommands {
		fmt.Fprintf(w, "  %-10s\n", name)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "operations:")
	reg := Registry()
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-10s %s\n", name, reg[name].Summary)
	}
}
