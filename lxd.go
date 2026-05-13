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

// ContainerManager manages the lifecycle of a container and command
// execution within it.
type ContainerManager interface {
	// Launch creates and starts a new container using the given Ubuntu
	// image alias (e.g. "24.04").
	Launch(image string) error

	// Exec runs a command inside the container and returns the combined
	// stdout/stderr output and exit code.
	Exec(ctx context.Context, command string) (*ExecResult, error)

	// Delete force-removes the container.
	Delete() error

	// Name returns the container name.
	Name() string
}

// LXDManager implements ContainerManager using the lxc CLI.
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

// Launch creates and starts an LXD container using the given Ubuntu
// image alias. For example, image "24.04" launches
// "ubuntu:24.04".
func (m *LXDManager) Launch(image string) error {
	if m.running {
		return fmt.Errorf("container %s is already running", m.name)
	}

	imageRef := "ubuntu:" + image
	cmd := m.execCommand(context.Background(), "lxc", "launch", imageRef, m.name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("lxc launch: %s", msg)
		}
		return fmt.Errorf("lxc launch: %w", err)
	}

	// Wait for the container's network to come up by polling for an
	// IPv4 address on eth0 (up to 30 seconds).
	if err := m.waitForNetwork(30 * time.Second); err != nil {
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
		return nil, fmt.Errorf("container %s is not running", m.name)
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

// waitForNetwork polls the container until an IPv4 address appears on
// eth0, or until the timeout expires.
func (m *LXDManager) waitForNetwork(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := m.execCommand(context.Background(), "lxc", "exec", m.name, "--", "ip", "-4", "addr", "show", "dev", "eth0")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err == nil {
			if strings.Contains(out.String(), "inet ") {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for network in container %s", m.name)
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
