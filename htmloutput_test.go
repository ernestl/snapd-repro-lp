package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavePromptHTML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-prompt.html")

	title := "Execution Prompt — Bug #12345"
	systemPrompt := "You are a <test> agent.\n## Instructions\n- Do things & stuff"
	userMessage := "Reproduce bug #12345: snap refresh \"hangs\""

	err := SavePromptHTML(path, title, systemPrompt, userMessage)
	if err != nil {
		t.Fatalf("SavePromptHTML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	content := string(data)

	// Check basic HTML structure.
	if !strings.Contains(content, "<!DOCTYPE html>") {
		t.Error("missing DOCTYPE")
	}
	if !strings.Contains(content, "<title>Execution Prompt — Bug #12345</title>") {
		t.Error("missing or incorrect title")
	}

	// Check that content is HTML-escaped.
	if !strings.Contains(content, "&lt;test&gt;") {
		t.Error("system prompt angle brackets not escaped")
	}
	if !strings.Contains(content, "&amp; stuff") {
		t.Error("system prompt ampersand not escaped")
	}
	if !strings.Contains(content, "&#34;hangs&#34;") {
		t.Error("user message quotes not escaped")
	}

	// Check sections exist.
	if !strings.Contains(content, "System Prompt") {
		t.Error("missing System Prompt section")
	}
	if !strings.Contains(content, "User Message") {
		t.Error("missing User Message section")
	}
}

func TestFileHyperlink(t *testing.T) {
	link := fileHyperlink("bug-123/prompt.html")

	// Should contain OSC 8 escape sequences.
	if !strings.Contains(link, "\033]8;;") {
		t.Error("missing OSC 8 opening sequence")
	}
	// Should contain file:// URL with absolute path.
	if !strings.Contains(link, "file://") {
		t.Error("missing file:// URL")
	}
	// Should contain the original relative path as display text.
	if !strings.Contains(link, "bug-123/prompt.html") {
		t.Error("missing display text")
	}
}
