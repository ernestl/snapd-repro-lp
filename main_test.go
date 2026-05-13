package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestRootShowsHelp(t *testing.T) {
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("reproduce")) {
		t.Fatal("expected help to mention reproduce subcommand")
	}
}

func TestReproduceWritesJSON(t *testing.T) {
	msgs := messagesResponse{
		Entries: []Message{
			{Subject: "Bug report", Content: "it crashes", DateCreated: "2024-01-01T00:00:00Z", OwnerLink: "https://api.launchpad.net/devel/~reporter"},
		},
	}
	attachments := &attachmentsResponse{
		Entries: []attachmentEntry{
			{Title: "crash.log", Type: "Unspecified", DataLink: "PLACEHOLDER"},
		},
	}
	bug := &bugResponse{
		Bug: Bug{
			ID:          12345,
			Title:       "snapd crashes",
			Description: "snap refresh crashes",
			Tags:        []string{"snapd"},
			WebLink:     "https://bugs.launchpad.net/snapd/+bug/12345",
		},
	}
	ts := newTestServerWithOpts(testServerOpts{
		bug:         bug,
		msgs:        msgs,
		attachments: attachments,
		fileData:    map[string]string{"/files/crash.log": "crash log content"},
	})
	defer ts.Close()
	bug.MessagesCollectionLink = ts.URL + "/bugs/12345/messages"
	bug.AttachmentsCollectionLink = ts.URL + "/bugs/12345/attachments"
	attachments.Entries[0].DataLink = ts.URL + "/files/crash.log"

	tmpDir := t.TempDir()
	b, err := fetchBug("12345", ts.URL)
	if err != nil {
		t.Fatalf("fetchBug: %v", err)
	}

	// Create bug subdirectory.
	bugDir := filepath.Join(tmpDir, "bug-12345")
	attachmentsDir := filepath.Join(bugDir, "attachments")
	if err := os.MkdirAll(attachmentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Download attachments.
	if err := DownloadAttachments(b.Attachments, attachmentsDir); err != nil {
		t.Fatalf("DownloadAttachments: %v", err)
	}

	// Download attachments.
	if err := DownloadAttachments(b.Attachments, attachmentsDir); err != nil {
		t.Fatalf("DownloadAttachments: %v", err)
	}

	// Write JSON.
	outFile := filepath.Join(bugDir, "bug-12345.json")
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(outFile, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read back and verify bug fields.
	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got Bug
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 12345 {
		t.Errorf("ID = %d, want 12345", got.ID)
	}
	if got.Title != "snapd crashes" {
		t.Errorf("Title = %q, want %q", got.Title, "snapd crashes")
	}
	if len(got.Messages) != 1 {
		t.Fatalf("Messages = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Author != "reporter" {
		t.Errorf("Author = %q, want %q", got.Messages[0].Author, "reporter")
	}

	// Verify attachments in JSON.
	if len(got.Attachments) != 1 {
		t.Fatalf("Attachments = %d, want 1", len(got.Attachments))
	}
	if got.Attachments[0].Title != "crash.log" {
		t.Errorf("Attachment Title = %q, want %q", got.Attachments[0].Title, "crash.log")
	}
	if got.Attachments[0].FilePath == "" {
		t.Error("Attachment FilePath should not be empty")
	}

	// Verify the attachment file was actually downloaded.
	attachData, err := os.ReadFile(got.Attachments[0].FilePath)
	if err != nil {
		t.Fatalf("reading attachment file: %v", err)
	}
	if string(attachData) != "crash log content" {
		t.Errorf("Attachment content = %q, want %q", string(attachData), "crash log content")
	}
}

func TestModelFromEnv(t *testing.T) {
	modelName = ""
	t.Setenv("OPENROUTER_MODEL", "custom/model")
	resolveModel()
	if modelName != "custom/model" {
		t.Errorf("model = %q, want %q", modelName, "custom/model")
	}
}

func TestModelFlagOverridesEnv(t *testing.T) {
	modelName = "cli-model"
	t.Setenv("OPENROUTER_MODEL", "env/model")
	resolveModel()
	if modelName != "cli-model" {
		t.Errorf("model = %q, want %q", modelName, "cli-model")
	}
}

func TestModelDefault(t *testing.T) {
	modelName = ""
	t.Setenv("OPENROUTER_MODEL", "")
	resolveModel()
	if modelName != "anthropic/claude-sonnet-4" {
		t.Errorf("model = %q, want %q", modelName, "anthropic/claude-sonnet-4")
	}
}
