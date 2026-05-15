package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"time"
)

// ExecResult holds the result of a command executed in a container.
type ExecResult struct {
	Output   string
	ExitCode int
}

// InstanceManager manages the lifecycle of an LXD instance and command
// execution within it.
type InstanceManager interface {
	// Launch creates and starts a new LXD instance using the given Ubuntu
	// image alias (e.g. "24.04"). The instanceType must be "vm" or
	// "container".
	Launch(image string, instanceType string) error

	// LaunchCached is like Launch but uses a golden-VM snapshot cache.
	// It returns a CacheStatus indicating which path was taken (hit,
	// miss, or fallback to a plain Launch).
	LaunchCached(image string, instanceType string) (CacheStatus, error)

	// Exec runs a command inside the instance and returns the combined
	// stdout/stderr output and exit code.
	Exec(ctx context.Context, command string) (*ExecResult, error)

	// Delete force-removes the instance.
	Delete() error

	// Name returns the instance name.
	Name() string
}

// LXDManager implements InstanceManager using the lxc CLI.
type LXDManager struct {
	name    string
	running bool
	// execCommand is the function used to create exec.Cmd. Overridable
	// for testing.
	execCommand func(cxt context.Context, name string, arg ...string) *exec.Cmd
}

// NewLXDManager creates a new LXD container manager. The container name
// is generated as "snapd-repro-<random>".
func NewLXDManager() *LXDManager {
	return &LXDManager{
		name:        generateContainerName(),
		execCommand: exec.CommandContext,
	}
}

// NewLXDManagerFromName creates an LXD manager for an existing container
// that is already running. Use this to exec commands in or delete a
// container that was launched separately.
func NewLXDManagerFromName(name string) *LXDManager {
	return &LXDManager{
		name:        name,
		running:     true,
		execCommand: exec.CommandContext,
	}
}

// Name returns the container name.
func (m *LXDManager) Name() string {
	return m.name
}

// Launch creates and starts an LXD instance using the given Ubuntu
// image alias. For example, image "24.04" launches "ubuntu:24.04".
// If instanceType is "vm", the instance is launched as a virtual machine
// (--vm flag). If instanceType is "container" or empty, it is launched
// as a container.
func (m *LXDManager) Launch(image string, instanceType string) error {
	if m.running {
		return fmt.Errorf("instance %s is already running", m.name)
	}

	imageRef := "ubuntu:" + image
	args := []string{"launch", imageRef, m.name}
	if instanceType == "vm" {
		args = append(args, "--vm")
	}

	cmd := m.execCommand(context.Background(), "lxc", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lxc launch: %s", msg)
		}
		return fmt.Errorf("lxc launch: %w", err)
	}

	// Wait for the instance's network to come up by polling for an
	// IPv4 address. VMs need more time to boot.
	timeout := 30 * time.Second
	if instanceType == "vm" {
		timeout = 120 * time.Second
	}
	if err := m.waitForNetwork(timeout, instanceType); err != nil {
		// Best-effort cleanup on failure.
		_ = m.Delete()
		return err
	}

	m.running = true
	return nil
}

// Exec runs a shell command inside the container. The command is
// executed via "lxc exec <name> -- bash -c <command>".
func (m *LXDManager) Exec(context context.Context, command string) (*ExecResult, error) {
	if !m.running {
		return nil, fmt.Errorf("instance %s is not running", m.name)
	}

	cmd := m.execCommand(context, "lxc", "exec", m.name, "--", "bash", "-c", command)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	result := &ExecResult{
		Output: output.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return nil, fmt.Errorf("lxc exec: %w", err)
	}

	return result, nil
}

// Delete force-removes the container.
func (m *LXDManager) Delete() error {
	cmd := m.execCommand(context.Background(), "lxc", "delete", "--force", m.name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lxc delete: %s", msg)
		}
		return fmt.Errorf("lxc delete: %w", err)
	}

	m.running = false
	return nil
}

// waitForNetwork polls the instance until an IPv4 address appears,
// or until the timeout expires. For VMs it uses "lxc list" which
// works before the LXD agent is ready; for containers it uses
// "lxc exec" to check eth0 directly.
func (m *LXDManager) waitForNetwork(timeout time.Duration, instanceType string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var hasIP bool
		if instanceType == "vm" {
			cmd := m.execCommand(context.Background(), "lxc", "list", m.name, "--format", "csv", "-c", "4")
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Run(); err == nil {
				line := strings.TrimSpace(out.String())
				if line != "" && !strings.HasPrefix(line, "No") {
					parts := strings.Fields(line)
					for _, p := range parts {
						if strings.Contains(p, ".") && !strings.HasPrefix(p, "127.") {
							hasIP = true
							break
						}
					}
				}
			}
		} else {
			cmd := m.execCommand(context.Background(), "lxc", "exec", m.name, "--", "ip", "-4", "addr", "show", "dev", "eth0")
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Run(); err == nil {
				hasIP = strings.Contains(out.String(), "inet ")
			}
		}
		if hasIP {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	kind := "container"
	if instanceType == "vm" {
		kind = "VM"
	}
	return fmt.Errorf("timeout waiting for network in %s %s", kind, m.name)
}

// --- VM snapshot cache ---
//
// To avoid the full boot + cloud-init + snap-seeding wait on every run,
// we maintain "golden" base VMs with a snapshot of the fully-initialised
// state. On cache hit we copy from the snapshot and start; on cache miss
// we create the golden VM first.

const goldenSnapshotName = "ready"

// goldenVMName returns the name of the golden base VM for a given
// Ubuntu version, e.g. "snapd-repro-base-2404" for version "24.04".
func goldenVMName(version string) string {
	return "snapd-repro-base-" + strings.ReplaceAll(version, ".", "")
}

// goldenVMExists checks whether an LXD instance with the given name
// exists by listing all instance names.
func (m *LXDManager) goldenVMExists(name string) (bool, error) {
	cmd := m.execCommand(context.Background(), "lxc", "list", "--format=csv", "-c", "n")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("lxc list: %w", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// waitForCloudInit waits for cloud-init to finish inside the instance.
func (m *LXDManager) waitForCloudInit(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := m.Exec(ctx, "cloud-init status --wait")
	if err != nil {
		return fmt.Errorf("waiting for cloud-init: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("cloud-init failed (exit %d): %s", result.ExitCode, result.Output)
	}
	return nil
}

// waitForSnapSeeding waits for snap seeding to complete inside the
// instance.
func (m *LXDManager) waitForSnapSeeding(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := m.Exec(ctx, "snap wait system seed.loaded")
	if err != nil {
		return fmt.Errorf("waiting for snap seeding: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("snap seeding failed (exit %d): %s", result.ExitCode, result.Output)
	}
	return nil
}

// stop gracefully stops the instance.
func (m *LXDManager) stop() error {
	cmd := m.execCommand(context.Background(), "lxc", "stop", m.name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lxc stop: %s", msg)
		}
		return fmt.Errorf("lxc stop: %w", err)
	}
	m.running = false
	return nil
}

// snapshotInstance creates a snapshot of this instance with the given
// name.
func (m *LXDManager) snapshotInstance(snapName string) error {
	cmd := m.execCommand(context.Background(), "lxc", "snapshot", m.name, snapName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lxc snapshot: %s", msg)
		}
		return fmt.Errorf("lxc snapshot: %w", err)
	}
	return nil
}

// copyFromSnapshot creates this instance as a copy of
// <instance>/<snapshot>.
func (m *LXDManager) copyFromSnapshot(instance, snapName string) error {
	source := instance + "/" + snapName
	cmd := m.execCommand(context.Background(), "lxc", "copy", source, m.name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lxc copy: %s", msg)
		}
		return fmt.Errorf("lxc copy: %w", err)
	}
	return nil
}

// startInstance starts a stopped or copied instance.
func (m *LXDManager) startInstance() error {
	cmd := m.execCommand(context.Background(), "lxc", "start", m.name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lxc start: %s", msg)
		}
		return fmt.Errorf("lxc start: %w", err)
	}
	return nil
}

// ensureGoldenVM creates a fully-initialised golden VM for the given
// version if one does not already exist. The golden VM is launched,
// booted until cloud-init and snap seeding complete, stopped, and then
// a snapshot named "ready" is taken.
func (m *LXDManager) ensureGoldenVM(goldenName, image, instanceType string) error {
	golden := &LXDManager{name: goldenName, execCommand: m.execCommand}

	if err := golden.Launch(image, instanceType); err != nil {
		return fmt.Errorf("launching golden VM: %w", err)
	}

	ctx := context.Background()
	if err := golden.waitForCloudInit(ctx, 5*time.Minute); err != nil {
		_ = golden.Delete()
		return fmt.Errorf("golden VM cloud-init: %w", err)
	}
	if err := golden.waitForSnapSeeding(ctx, 5*time.Minute); err != nil {
		_ = golden.Delete()
		return fmt.Errorf("golden VM snap seeding: %w", err)
	}

	if err := golden.stop(); err != nil {
		_ = golden.Delete()
		return fmt.Errorf("stopping golden VM: %w", err)
	}

	if err := golden.snapshotInstance(goldenSnapshotName); err != nil {
		_ = golden.Delete()
		return fmt.Errorf("snapshotting golden VM: %w", err)
	}

	return nil
}

// CacheStatus indicates which launch path was taken by LaunchCached.
type CacheStatus int

const (
	// CacheHit means the instance was restored from a cached golden
	// VM snapshot.
	CacheHit CacheStatus = iota
	// CacheMiss means a golden VM was created first, then the instance
	// was restored from it.
	CacheMiss
	// CacheFallback means caching failed and a normal launch was used.
	CacheFallback
)

// LaunchCached creates and starts an LXD instance using a cached golden
// VM snapshot when available. On cache hit the instance is copied from
// the snapshot and started; on cache miss a golden VM is created first.
// If any cache operation fails, it falls back to a normal Launch.
// The returned CacheStatus indicates which path was taken.
func (m *LXDManager) LaunchCached(image string, instanceType string) (CacheStatus, error) {
	if m.running {
		return CacheFallback, fmt.Errorf("instance %s is already running", m.name)
	}

	golden := goldenVMName(image)

	// Check whether the golden VM exists.
	exists, err := m.goldenVMExists(golden)
	if err != nil {
		// Cannot check — fall back to normal launch.
		return CacheFallback, m.Launch(image, instanceType)
	}

	// Create the golden VM if it does not exist.
	status := CacheHit
	if !exists {
		if err := m.ensureGoldenVM(golden, image, instanceType); err != nil {
			return CacheFallback, m.Launch(image, instanceType)
		}
		status = CacheMiss
	}

	// Copy from the golden snapshot.
	if err := m.copyFromSnapshot(golden, goldenSnapshotName); err != nil {
		return CacheFallback, m.Launch(image, instanceType)
	}

	// Start the copied instance and wait for network.
	if err := m.startInstance(); err != nil {
		_ = m.Delete()
		return CacheFallback, m.Launch(image, instanceType)
	}

	timeout := 30 * time.Second
	if instanceType == "vm" {
		timeout = 120 * time.Second
	}
	if err := m.waitForNetwork(timeout, instanceType); err != nil {
		_ = m.Delete()
		return status, err
	}

	m.running = true
	return status, nil
}

// generateContainerName returns a name like "snapd-repro-a1b2c3".
func generateContainerName() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return "snapd-repro-" + string(b)
}
