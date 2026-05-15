package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
		// Determine column type: -c n (name listing) vs -c 4 (IPv4).
		colType := ""
		for i, a := range args {
			if a == "-c" && i+1 < len(args) {
				colType = args[i+1]
				break
			}
		}

		if colType == "n" {
			// Name listing for golden VM existence check.
			if behavior == "list_fail" {
				fmt.Fprintf(os.Stderr, "Error: list failed")
				os.Exit(1)
			}
			names := os.Getenv("LXC_GOLDEN_NAMES")
			if names != "" {
				fmt.Print(names)
			}
			os.Exit(0)
		}

		// IPv4 listing (network check).
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

			// cloud-init wait
			if strings.Contains(command, "cloud-init") {
				if behavior == "cloud_init_fail" {
					fmt.Println("error: cloud-init not available")
					os.Exit(1)
				}
				fmt.Println("status: done")
				os.Exit(0)
			}

			// snap seeding wait
			if strings.Contains(command, "snap wait") {
				if behavior == "snap_seed_fail" {
					fmt.Println("error: snap seeding timed out")
					os.Exit(1)
				}
				os.Exit(0)
			}

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

	case "stop":
		if behavior == "stop_fail" {
			fmt.Fprintf(os.Stderr, "Error: Instance not found")
			os.Exit(1)
		}
		os.Exit(0)

	case "snapshot":
		if behavior == "snapshot_fail" {
			fmt.Fprintf(os.Stderr, "Error: Snapshot failed")
			os.Exit(1)
		}
		os.Exit(0)

	case "copy":
		if behavior == "copy_fail" {
			fmt.Fprintf(os.Stderr, "Error: Not found")
			os.Exit(1)
		}
		os.Exit(0)

	case "start":
		if behavior == "start_fail" {
			fmt.Fprintf(os.Stderr, "Error: Instance already running")
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

// --- VM snapshot cache tests ---

// fakeExecCommandEnv returns a function that creates exec.Cmd pointing
// back to TestHelperProcess with arbitrary environment variables set.
func fakeExecCommandEnv(envs ...string) func(_ context.Context, name string, args ...string) *exec.Cmd {
	return func(_ context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		env := append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		env = append(env, envs...)
		cmd.Env = env
		return cmd
	}
}

func TestGoldenVMName(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{"24.04", "snapd-repro-base-2404"},
		{"22.04", "snapd-repro-base-2204"},
		{"20.04", "snapd-repro-base-2004"},
		{"25.10", "snapd-repro-base-2510"},
	}
	for _, tt := range tests {
		got := goldenVMName(tt.version)
		if got != tt.want {
			t.Errorf("goldenVMName(%q) = %q, want %q", tt.version, got, tt.want)
		}
	}
}

func TestGoldenVMExists(t *testing.T) {
	m := &LXDManager{
		name:        "test-instance",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=success", "LXC_GOLDEN_NAMES=snapd-repro-base-2404\nsnapd-repro-base-2204"),
	}
	exists, err := m.goldenVMExists("snapd-repro-base-2404")
	if err != nil {
		t.Fatalf("goldenVMExists error: %v", err)
	}
	if !exists {
		t.Error("expected golden VM to exist")
	}
}

func TestGoldenVMNotExists(t *testing.T) {
	m := &LXDManager{
		name:        "test-instance",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=success", "LXC_GOLDEN_NAMES="),
	}
	exists, err := m.goldenVMExists("snapd-repro-base-2404")
	if err != nil {
		t.Fatalf("goldenVMExists error: %v", err)
	}
	if exists {
		t.Error("expected golden VM to not exist")
	}
}

func TestGoldenVMExistsListFails(t *testing.T) {
	m := &LXDManager{
		name:        "test-instance",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=list_fail"),
	}
	_, err := m.goldenVMExists("snapd-repro-base-2404")
	if err == nil {
		t.Fatal("expected error when list fails")
	}
}

func TestStop(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		running:     true,
		execCommand: fakeExecCommand("success"),
	}
	if err := m.stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if m.running {
		t.Error("expected running = false after stop")
	}
}

func TestStopFail(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		running:     true,
		execCommand: fakeExecCommand("stop_fail"),
	}
	err := m.stop()
	if err == nil {
		t.Fatal("expected error for stop failure")
	}
	if !strings.Contains(err.Error(), "Instance not found") {
		t.Errorf("error = %q, want 'Instance not found'", err.Error())
	}
}

func TestSnapshotInstance(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		execCommand: fakeExecCommand("success"),
	}
	if err := m.snapshotInstance("ready"); err != nil {
		t.Fatalf("snapshotInstance failed: %v", err)
	}
}

func TestSnapshotInstanceFail(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		execCommand: fakeExecCommand("snapshot_fail"),
	}
	err := m.snapshotInstance("ready")
	if err == nil {
		t.Fatal("expected error for snapshot failure")
	}
	if !strings.Contains(err.Error(), "Snapshot failed") {
		t.Errorf("error = %q, want 'Snapshot failed'", err.Error())
	}
}

func TestCopyFromSnapshot(t *testing.T) {
	m := &LXDManager{
		name:        "new-instance",
		execCommand: fakeExecCommand("success"),
	}
	if err := m.copyFromSnapshot("snapd-repro-base-2404", "ready"); err != nil {
		t.Fatalf("copyFromSnapshot failed: %v", err)
	}
}

func TestCopyFromSnapshotFail(t *testing.T) {
	m := &LXDManager{
		name:        "new-instance",
		execCommand: fakeExecCommand("copy_fail"),
	}
	err := m.copyFromSnapshot("snapd-repro-base-2404", "ready")
	if err == nil {
		t.Fatal("expected error for copy failure")
	}
	if !strings.Contains(err.Error(), "Not found") {
		t.Errorf("error = %q, want 'Not found'", err.Error())
	}
}

func TestStartInstance(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		execCommand: fakeExecCommand("success"),
	}
	if err := m.startInstance(); err != nil {
		t.Fatalf("startInstance failed: %v", err)
	}
}

func TestStartInstanceFail(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		execCommand: fakeExecCommand("start_fail"),
	}
	err := m.startInstance()
	if err == nil {
		t.Fatal("expected error for start failure")
	}
	if !strings.Contains(err.Error(), "Instance already running") {
		t.Errorf("error = %q, want 'Instance already running'", err.Error())
	}
}

func TestWaitForCloudInit(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		running:     true,
		execCommand: fakeExecCommand("success"),
	}
	err := m.waitForCloudInit(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("waitForCloudInit failed: %v", err)
	}
}

func TestWaitForCloudInitFail(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		running:     true,
		execCommand: fakeExecCommand("cloud_init_fail"),
	}
	err := m.waitForCloudInit(context.Background(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error for cloud-init failure")
	}
	if !strings.Contains(err.Error(), "cloud-init") {
		t.Errorf("error = %q, want cloud-init related error", err.Error())
	}
}

func TestWaitForSnapSeeding(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		running:     true,
		execCommand: fakeExecCommand("success"),
	}
	err := m.waitForSnapSeeding(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("waitForSnapSeeding failed: %v", err)
	}
}

func TestWaitForSnapSeedingFail(t *testing.T) {
	m := &LXDManager{
		name:        "test-vm",
		running:     true,
		execCommand: fakeExecCommand("snap_seed_fail"),
	}
	err := m.waitForSnapSeeding(context.Background(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error for snap seeding failure")
	}
	if !strings.Contains(err.Error(), "snap seeding") {
		t.Errorf("error = %q, want snap seeding related error", err.Error())
	}
}

func TestLaunchCachedHit(t *testing.T) {
	// Golden VM exists — should copy from snapshot and start.
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=success", "LXC_GOLDEN_NAMES=snapd-repro-base-2404"),
	}
	status, err := m.LaunchCached("24.04", "vm")
	if err != nil {
		t.Fatalf("LaunchCached (cache hit) failed: %v", err)
	}
	if status != CacheHit {
		t.Errorf("status = %d, want CacheHit (%d)", status, CacheHit)
	}
	if !m.running {
		t.Error("expected running = true after LaunchCached")
	}
}

func TestLaunchCachedMiss(t *testing.T) {
	// Golden VM does not exist — should create it, then copy+start.
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=success", "LXC_GOLDEN_NAMES="),
	}
	status, err := m.LaunchCached("24.04", "vm")
	if err != nil {
		t.Fatalf("LaunchCached (cache miss) failed: %v", err)
	}
	if status != CacheMiss {
		t.Errorf("status = %d, want CacheMiss (%d)", status, CacheMiss)
	}
	if !m.running {
		t.Error("expected running = true after LaunchCached")
	}
}

func TestLaunchCachedCopyFails(t *testing.T) {
	// Golden VM exists but copy fails — should fall back to normal Launch.
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=copy_fail", "LXC_GOLDEN_NAMES=snapd-repro-base-2404"),
	}
	// copy_fail makes lxc copy fail, but lxc launch (fallback) also
	// needs to succeed. The helper only fails on "launch_fail" for
	// the launch subcommand, so this works.
	status, err := m.LaunchCached("24.04", "vm")
	// The fallback Launch() will also fail because waitForNetwork
	// calls lxc list -c 4 which succeeds, so it should work.
	// However, the copy_fail behavior doesn't affect launch or list.
	if err != nil {
		t.Fatalf("LaunchCached (copy fails, fallback) failed: %v", err)
	}
	if status != CacheFallback {
		t.Errorf("status = %d, want CacheFallback (%d)", status, CacheFallback)
	}
	if !m.running {
		t.Error("expected running = true after LaunchCached fallback")
	}
}

func TestLaunchCachedListFails(t *testing.T) {
	// Cannot check golden VM existence — should fall back to normal Launch.
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=list_fail"),
	}
	// list_fail makes lxc list -c n fail, but fallback Launch() uses
	// lxc list -c 4 which also fails. We need different behavior...
	// Actually, list_fail affects the -c n path only. The -c 4 path
	// falls through to the normal IP listing. Let's verify.
	status, err := m.LaunchCached("24.04", "vm")
	// The list helper with list_fail only fails when colType == "n".
	// The network check (colType == "4") still returns an IP.
	if err != nil {
		t.Fatalf("LaunchCached (list fails, fallback) failed: %v", err)
	}
	if status != CacheFallback {
		t.Errorf("status = %d, want CacheFallback (%d)", status, CacheFallback)
	}
	if !m.running {
		t.Error("expected running = true after LaunchCached fallback")
	}
}

func TestLaunchCachedAlreadyRunning(t *testing.T) {
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		running:     true,
		execCommand: fakeExecCommand("success"),
	}
	_, err := m.LaunchCached("24.04", "vm")
	if err == nil {
		t.Fatal("expected error for LaunchCached on running instance")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want 'already running'", err.Error())
	}
}

func TestLaunchCachedStartFails(t *testing.T) {
	// Golden exists, copy succeeds, but start fails — should fall back.
	// With start_fail, the fallback Launch() uses lxc launch (not
	// lxc start), so it succeeds.
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommandEnv("LXC_BEHAVIOR=start_fail", "LXC_GOLDEN_NAMES=snapd-repro-base-2404"),
	}
	status, err := m.LaunchCached("24.04", "vm")
	if err != nil {
		t.Fatalf("LaunchCached (start fails, fallback) failed: %v", err)
	}
	if status != CacheFallback {
		t.Errorf("status = %d, want CacheFallback (%d)", status, CacheFallback)
	}
	if !m.running {
		t.Error("expected running = true after LaunchCached fallback")
	}
}

func TestEnsureGoldenVMCloudInitFails(t *testing.T) {
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommand("cloud_init_fail"),
	}
	err := m.ensureGoldenVM("snapd-repro-base-2404", "24.04", "vm")
	if err == nil {
		t.Fatal("expected error when cloud-init fails")
	}
	if !strings.Contains(err.Error(), "cloud-init") {
		t.Errorf("error = %q, want cloud-init related error", err.Error())
	}
}

func TestEnsureGoldenVMSnapSeedFails(t *testing.T) {
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommand("snap_seed_fail"),
	}
	err := m.ensureGoldenVM("snapd-repro-base-2404", "24.04", "vm")
	if err == nil {
		t.Fatal("expected error when snap seeding fails")
	}
	if !strings.Contains(err.Error(), "snap seeding") {
		t.Errorf("error = %q, want snap seeding related error", err.Error())
	}
}

func TestEnsureGoldenVMStopFails(t *testing.T) {
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommand("stop_fail"),
	}
	err := m.ensureGoldenVM("snapd-repro-base-2404", "24.04", "vm")
	if err == nil {
		t.Fatal("expected error when stop fails")
	}
	if !strings.Contains(err.Error(), "stopping golden VM") {
		t.Errorf("error = %q, want 'stopping golden VM'", err.Error())
	}
}

func TestEnsureGoldenVMSnapshotFails(t *testing.T) {
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommand("snapshot_fail"),
	}
	err := m.ensureGoldenVM("snapd-repro-base-2404", "24.04", "vm")
	if err == nil {
		t.Fatal("expected error when snapshot fails")
	}
	if !strings.Contains(err.Error(), "snapshotting golden VM") {
		t.Errorf("error = %q, want 'snapshotting golden VM'", err.Error())
	}
}

func TestEnsureGoldenVMSuccess(t *testing.T) {
	m := &LXDManager{
		name:        "snapd-repro-abc123",
		execCommand: fakeExecCommand("success"),
	}
	err := m.ensureGoldenVM("snapd-repro-base-2404", "24.04", "vm")
	if err != nil {
		t.Fatalf("ensureGoldenVM failed: %v", err)
	}
}
