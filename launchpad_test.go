package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type testServerOpts struct {
	bug         *bugResponse
	msgs        messagesResponse
	attachments *attachmentsResponse
	fileData    map[string]string // path suffix -> content for attachment data endpoints
}

func newTestServerWithOpts(opts testServerOpts) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/bugs/12345", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(opts.bug)
	})
	mux.HandleFunc("/bugs/12345/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(opts.msgs)
	})
	if opts.attachments != nil {
		mux.HandleFunc("/bugs/12345/attachments", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(opts.attachments)
		})
	} else {
		// Return empty collection by default.
		mux.HandleFunc("/bugs/12345/attachments", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(attachmentsResponse{})
		})
	}
	for path, content := range opts.fileData {
		content := content // capture
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(content))
		})
	}
	return httptest.NewServer(mux)
}

func TestFetchBug(t *testing.T) {
	msgs := messagesResponse{
		TotalSize: 2,
		Entries: []Message{
			{Subject: "Bug report", Content: "Steps to reproduce...", DateCreated: "2024-01-01T00:00:00Z", OwnerLink: "https://api.launchpad.net/devel/~alice"},
			{Subject: "Re: Bug report", Content: "I can confirm this.", DateCreated: "2024-01-02T00:00:00Z", OwnerLink: "https://api.launchpad.net/devel/~bob"},
		},
	}

	bug := &bugResponse{
		Bug: Bug{
			ID:          12345,
			Title:       "snapd fails to refresh",
			Description: "After running snap refresh...",
			Tags:        []string{"snapd", "regression"},
			WebLink:     "https://bugs.launchpad.net/snapd/+bug/12345",
		},
	}

	attachments := &attachmentsResponse{
		Entries: []attachmentEntry{
			{Title: "debug.log", Type: "Unspecified", DataLink: "PLACEHOLDER"},
		},
	}

	ts := newTestServerWithOpts(testServerOpts{
		bug:         bug,
		msgs:        msgs,
		attachments: attachments,
		fileData:    map[string]string{"/files/debug.log": "log content"},
	})
	defer ts.Close()
	bug.MessagesCollectionLink = ts.URL + "/bugs/12345/messages"
	bug.AttachmentsCollectionLink = ts.URL + "/bugs/12345/attachments"
	attachments.Entries[0].DataLink = ts.URL + "/files/debug.log"

	got, err := fetchBug("12345", ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ID != 12345 {
		t.Errorf("ID = %d, want 12345", got.ID)
	}
	if got.Title != "snapd fails to refresh" {
		t.Errorf("Title = %q, want %q", got.Title, "snapd fails to refresh")
	}
	if len(got.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(got.Messages))
	}
	if got.Messages[1].Content != "I can confirm this." {
		t.Errorf("Message[1].Content = %q", got.Messages[1].Content)
	}
	if got.Messages[0].Author != "alice" {
		t.Errorf("Message[0].Author = %q, want %q", got.Messages[0].Author, "alice")
	}
	if got.Messages[1].Author != "bob" {
		t.Errorf("Message[1].Author = %q, want %q", got.Messages[1].Author, "bob")
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(got.Attachments))
	}
	if got.Attachments[0].Title != "debug.log" {
		t.Errorf("Attachment[0].Title = %q, want %q", got.Attachments[0].Title, "debug.log")
	}
	if got.Attachments[0].DataLink == "" {
		t.Error("Attachment[0].DataLink should be set")
	}
}

func TestFetchBugFromURL(t *testing.T) {
	msgs := messagesResponse{Entries: []Message{{Content: "test"}}}
	bug := &bugResponse{
		Bug: Bug{ID: 12345, Title: "test bug"},
	}

	ts := newTestServerWithOpts(testServerOpts{bug: bug, msgs: msgs})
	defer ts.Close()
	bug.MessagesCollectionLink = ts.URL + "/bugs/12345/messages"
	bug.AttachmentsCollectionLink = ts.URL + "/bugs/12345/attachments"

	got, err := fetchBug("https://bugs.launchpad.net/snapd/+bug/12345", ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 12345 {
		t.Errorf("ID = %d, want 12345", got.ID)
	}
}

func TestParseBugRef(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"12345", "12345", false},
		{"https://bugs.launchpad.net/snapd/+bug/99999", "99999", false},
		{"https://bugs.launchpad.net/ubuntu/+source/snapd/+bug/77777", "77777", false},
		{"", "", true},
		{"abc", "", true},
		{"https://example.com/not-a-bug", "", true},
	}
	for _, tt := range tests {
		got, err := parseBugRef(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseBugRef(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseBugRef(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFetchBugNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	_, err := fetchBug("99999", ts.URL)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestAuthorFromLink(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://api.launchpad.net/devel/~alice", "alice"},
		{"https://api.launchpad.net/devel/~some-user", "some-user"},
		{"", ""},
	}
	for _, tt := range tests {
		got := authorFromLink(tt.input)
		if got != tt.want {
			t.Errorf("authorFromLink(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFetchAttachmentMetadata(t *testing.T) {
	attachments := &attachmentsResponse{
		Entries: []attachmentEntry{
			{Title: "debug.log", Type: "Unspecified", DataLink: "http://example.com/debug.log"},
			{Title: "state.json", Type: "Unspecified", DataLink: "http://example.com/state.json"},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/attachments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(attachments) //nolint:errcheck
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	got, err := fetchAttachmentMetadata(ts.URL + "/attachments")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d attachments, want 2", len(got))
	}
	if got[0].Title != "debug.log" {
		t.Errorf("Attachment[0].Title = %q, want %q", got[0].Title, "debug.log")
	}
	if got[1].Title != "state.json" {
		t.Errorf("Attachment[1].Title = %q, want %q", got[1].Title, "state.json")
	}
	if got[0].DataLink == "" {
		t.Error("Attachment[0].DataLink should be set")
	}
}

func TestFetchAttachmentMetadataEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/attachments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(attachmentsResponse{}) //nolint:errcheck
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	got, err := fetchAttachmentMetadata(ts.URL + "/attachments")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d attachments, want 0", len(got))
	}
}

func TestDownloadAttachments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/files/debug.log", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some debug output"))
	})
	mux.HandleFunc("/files/state.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"key":"value"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	tmpDir := t.TempDir()
	attachments := []Attachment{
		{Title: "debug.log", Type: "Unspecified", DataLink: ts.URL + "/files/debug.log"},
		{Title: "state.json", Type: "Unspecified", DataLink: ts.URL + "/files/state.json"},
	}

	err := DownloadAttachments(attachments, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify files exist and have correct content.
	for i, want := range []struct {
		name    string
		content string
	}{
		{"debug.log", "some debug output"},
		{"state.json", `{"key":"value"}`},
	} {
		if attachments[i].FilePath == "" {
			t.Errorf("Attachment[%d].FilePath is empty", i)
			continue
		}
		data, err := os.ReadFile(attachments[i].FilePath)
		if err != nil {
			t.Errorf("reading %s: %v", attachments[i].FilePath, err)
			continue
		}
		if string(data) != want.content {
			t.Errorf("Attachment[%d] content = %q, want %q", i, string(data), want.content)
		}
	}
}

func TestDownloadAttachmentsDeduplication(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/files/a", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
	})
	mux.HandleFunc("/files/b", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("second"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	tmpDir := t.TempDir()
	attachments := []Attachment{
		{Title: "report.log", Type: "Unspecified", DataLink: ts.URL + "/files/a"},
		{Title: "report.log", Type: "Unspecified", DataLink: ts.URL + "/files/b"},
	}

	err := DownloadAttachments(attachments, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First should be report.log, second should be report_1.log
	if filepath.Base(attachments[0].FilePath) != "report.log" {
		t.Errorf("Attachment[0] filename = %q, want %q", filepath.Base(attachments[0].FilePath), "report.log")
	}
	if filepath.Base(attachments[1].FilePath) != "report_1.log" {
		t.Errorf("Attachment[1] filename = %q, want %q", filepath.Base(attachments[1].FilePath), "report_1.log")
	}

	// Verify contents are different.
	data0, _ := os.ReadFile(attachments[0].FilePath)
	data1, _ := os.ReadFile(attachments[1].FilePath)
	if string(data0) != "first" {
		t.Errorf("Attachment[0] content = %q, want %q", string(data0), "first")
	}
	if string(data1) != "second" {
		t.Errorf("Attachment[1] content = %q, want %q", string(data1), "second")
	}
}

func TestDownloadAttachmentsEmptyDataLink(t *testing.T) {
	tmpDir := t.TempDir()
	attachments := []Attachment{
		{Title: "nolink.txt", Type: "Unspecified", DataLink: ""},
	}

	err := DownloadAttachments(attachments, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attachments[0].FilePath != "" {
		t.Errorf("FilePath should be empty for attachment with no DataLink, got %q", attachments[0].FilePath)
	}
}
