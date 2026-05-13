package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestHelperProcess is invoked by the mock exec.Command calls. It is
// not a real test and exits immediately when GO_WANT_HELPER_PROCESS is
// not set.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	// Find the "--" separator that marks the start of the real command.
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}

	if len(args) == 0 {
		os.Exit(1)
	}

	// Dispatch based on the command and subcommand.
	switch args[0] {
	case "lxc":
		handleLXCHelper(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		os.Exit(1)
	}
}

func handleLXCHelper(args []string) {
	if len(args) == 0 {
		os.Exit(1)
	}

	behavior := os.Getenv("LXC_BEHAVIOR")

	switch args[0] {
	case "launch":
		if behavior == "launch_fail" {
			fmt.Fprintf(os.Stderr, "Error: Failed to create container")
			os.Exit(1)
		}
		// Success — no output needed.
		os.Exit(0)

	case "list":
		if behavior == "network_timeout" {
			// Simulate no network — output empty/no IP.
			fmt.Println(",,No,,")
			os.Exit(0)
		}
		// Simulate an instance with an IPv4 address.
		fmt.Println("10.0.0.5")
		os.Exit(0)

	case "exec":
		// args: exec <name> -- <cmd...>
		// Find the "--" separator.
		cmdStart := -1
		for i, a := range args {
			if a == "--" {
				cmdStart = i + 1
				break
			}
		}
		if cmdStart < 0 || cmdStart >= len(args) {
			os.Exit(1)
		}

		// Check if this is the network wait command (ip -4 addr show).
		remaining := args[cmdStart:]
		if len(remaining) >= 1 && remaining[0] == "ip" {
			if behavior == "network_timeout" {
				// Simulate no network — exit without "inet " in output.
				os.Exit(0)
			}
			fmt.Println("2: eth0    inet 10.0.0.5/24 brd 10.0.0.255 scope global eth0")
			os.Exit(0)
		}

		// For bash -c commands:
		if len(remaining) >= 2 && remaining[0] == "bash" && remaining[1] == "-c" {
			command := remaining[2]
			if behavior == "exec_fail" {
				_, _ = fmt.Fprintf(os.Stdout, "command not found: %s\n", command)
				os.Exit(127)
			}
			fmt.Printf("executed: %s\n", command)
			os.Exit(0)
		}
		os.Exit(0)

	case "delete":
		if behavior == "delete_fail" {
			fmt.Fprintf(os.Stderr, "Error: Container not found")
			os.Exit(1)
		}
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "unknown lxc subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// fakeExecCommand returns a function that creates exec.Cmd pointing back
// to TestHelperProcess, with the given behavior set via LXC_BEHAVIOR.
func fakeExecCommand(behavior string) func(_ context.Context, name string, args ...string) *exec.Cmd {
	return func(_ context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"LXC_BEHAVIOR="+behavior,
		)
		return cmd
	}
}

func newTestLXDManager(behavior string) *LXDManager {
	m := NewLXDManager()
	m.execCommand = fakeExecCommand(behavior)
	return m
}

func TestLXDManagerName(t *testing.T) {
	m := NewLXDManager()
	if !strings.HasPrefix(m.Name(), "snapd-repro-") {
		t.Errorf("Name = %q, want prefix %q", m.Name(), "snapd-repro-")
	}
	if len(m.Name()) != len("snapd-repro-")+6 {
		t.Errorf("Name length = %d, want %d", len(m.Name()), len("snapd-repro-")+6)
	}
}

func TestLXDManagerNameUniqueness(t *testing.T) {
	names := make(map[string]bool)
	for i := 0; i < 20; i++ {
		m := NewLXDManager()
		if names[m.Name()] {
			t.Errorf("duplicate name generated: %s", m.Name())
		}
		names[m.Name()] = true
	}
}

func TestLXDManagerLaunch(t *testing.T) {
	m := newTestLXDManager("success")
	if err := m.Launch("24.04", "container"); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	if !m.running {
		t.Error("expected running = true after Launch")
	}
}

func TestLXDManagerLaunchVM(t *testing.T) {
	m := newTestLXDManager("success")
	if err := m.Launch("24.04", "vm"); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	if !m.running {
		t.Error("expected running = true after Launch")
	}
}

func TestLXDManagerLaunchAlreadyRunning(t *testing.T) {
	m := newTestLXDManager("success")
	if err := m.Launch("24.04", "container"); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}

	err := m.Launch("24.04", "container")
	if err == nil {
		t.Fatal("expected error for double Launch")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want 'already running'", err.Error())
	}
}

func TestLXDManagerLaunchFail(t *testing.T) {
	m := newTestLXDManager("launch_fail")
	err := m.Launch("24.04", "container")
	if err == nil {
		t.Fatal("expected error for failed launch")
	}
	if !strings.Contains(err.Error(), "Failed to create container") {
		t.Errorf("error = %q, want 'Failed to create container'", err.Error())
	}
}

func TestLXDManagerExec(t *testing.T) {
	m := newTestLXDManager("success")
	if err := m.Launch("24.04", "container"); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}

	result, err := m.Exec(context.Background(), "snap list")
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Output, "executed: snap list") {
		t.Errorf("Output = %q, want 'executed: snap list'", result.Output)
	}
}

func TestLXDManagerExecNonZero(t *testing.T) {
	// Need to launch first with a behavior that lets launch succeed
	// but exec fail. Use a manager where we swap behavior after launch.
	launchM := newTestLXDManager("success")
	if err := launchM.Launch("24.04", "container"); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	// Now swap to exec_fail behavior.
	launchM.execCommand = fakeExecCommand("exec_fail")

	result, err := launchM.Exec(context.Background(), "nonexistent_command")
	if err != nil {
		t.Fatalf("Exec should not error on non-zero exit: %v", err)
	}
	if result.ExitCode != 127 {
		t.Errorf("ExitCode = %d, want 127", result.ExitCode)
	}
}

func TestLXDManagerExecNotRunning(t *testing.T) {
	m := newTestLXDManager("success")

	_, err := m.Exec(context.Background(), "snap list")
	if err == nil {
		t.Fatal("expected error for Exec on non-running container")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %q, want 'not running'", err.Error())
	}
}

func TestLXDManagerDelete(t *testing.T) {
	m := newTestLXDManager("success")
	if err := m.Launch("24.04", "container"); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}

	if err := m.Delete(); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if m.running {
		t.Error("expected running = false after Delete")
	}
}

func TestLXDManagerDeleteFail(t *testing.T) {
	m := newTestLXDManager("success")
	if err := m.Launch("24.04", "container"); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	m.execCommand = fakeExecCommand("delete_fail")

	err := m.Delete()
	if err == nil {
		t.Fatal("expected error for failed delete")
	}
	if !strings.Contains(err.Error(), "Container not found") {
		t.Errorf("error = %q, want 'Container not found'", err.Error())
	}
}

func TestGenerateContainerName(t *testing.T) {
	name := generateContainerName()
	if !strings.HasPrefix(name, "snapd-repro-") {
		t.Errorf("name = %q, want prefix %q", name, "snapd-repro-")
	}
	suffix := strings.TrimPrefix(name, "snapd-repro-")
	if len(suffix) != 6 {
		t.Errorf("suffix length = %d, want 6", len(suffix))
	}
	for _, c := range suffix {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			t.Errorf("suffix contains invalid char %q", string(c))
		}
	}
}

func TestNewLXDManagerFromName(t *testing.T) {
	m := NewLXDManagerFromName("my-existing-container")
	if m.Name() != "my-existing-container" {
		t.Errorf("Name = %q, want %q", m.Name(), "my-existing-container")
	}
	if !m.running {
		t.Error("expected running = true for existing container")
	}
}
