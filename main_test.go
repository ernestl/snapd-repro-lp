package main

import (
	"bytes"
	"testing"
)

func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

func TestReproduceRequiresArg(t *testing.T) {
	_, err := executeCommand("reproduce")
	if err == nil {
		t.Fatal("expected error when no bug ID provided")
	}
}

func TestReproduceWithBugID(t *testing.T) {
	out, err := executeCommand("reproduce", "12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected output")
	}
}

func TestRootShowsHelp(t *testing.T) {
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("reproduce")) {
		t.Fatal("expected help to mention reproduce subcommand")
	}
}
