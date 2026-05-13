package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultBaseURL = "https://api.launchpad.net/devel"

// Bug holds the relevant fields from a Launchpad bug report.
type Bug struct {
	ID          int          `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Tags        []string     `json:"tags"`
	WebLink     string       `json:"web_link"`
	Messages    []Message    `json:"messages"`
	Attachments []Attachment `json:"attachments"`
}

// Message holds a single comment/message on a Launchpad bug.
type Message struct {
	Subject     string `json:"subject"`
	Content     string `json:"content"`
	Author      string `json:"author"`
	DateCreated string `json:"date_created"`
	OwnerLink   string `json:"owner_link"`
}

// Attachment holds metadata and the local path of a downloaded bug attachment.
type Attachment struct {
	Title    string `json:"title"`
	Type     string `json:"type"`
	FilePath string `json:"file_path"`
	DataLink string `json:"-"`
}

type messagesResponse struct {
	Entries   []Message `json:"entries"`
	NextLink  string    `json:"next_collection_link"`
	TotalSize int       `json:"total_size"`
}

type attachmentEntry struct {
	Title    string `json:"title"`
	Type     string `json:"type"`
	DataLink string `json:"data_link"`
}

type attachmentsResponse struct {
	Entries  []attachmentEntry `json:"entries"`
	NextLink string            `json:"next_collection_link"`
}

type bugResponse struct {
	Bug
	MessagesCollectionLink    string `json:"messages_collection_link"`
	AttachmentsCollectionLink string `json:"attachments_collection_link"`
}

// FetchBug fetches a bug and its messages from the Launchpad API.
// bugRef can be a numeric ID or a full Launchpad bug URL.
func FetchBug(bugRef string) (*Bug, error) {
	return fetchBug(bugRef, defaultBaseURL)
}

func fetchBug(bugRef, baseURL string) (*Bug, error) {
	bugID, err := parseBugRef(bugRef)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/bugs/%s", baseURL, bugID)
	var br bugResponse
	if err := getJSON(url, &br); err != nil {
		return nil, fmt.Errorf("fetching bug %s: %w", bugID, err)
	}

	msgs, err := fetchMessages(br.MessagesCollectionLink)
	if err != nil {
		return nil, fmt.Errorf("fetching messages for bug %s: %w", bugID, err)
	}

	attachments, err := fetchAttachmentMetadata(br.AttachmentsCollectionLink)
	if err != nil {
		return nil, fmt.Errorf("fetching attachments for bug %s: %w", bugID, err)
	}

	bug := br.Bug
	bug.Messages = msgs
	bug.Attachments = attachments
	return &bug, nil
}

var lpURLPattern = regexp.MustCompile(`bugs\.launchpad\.net/.+/\+bug/(\d+)`)

func parseBugRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty bug reference")
	}

	// If it's a URL, extract the bug ID.
	if strings.Contains(ref, "launchpad.net") {
		m := lpURLPattern.FindStringSubmatch(ref)
		if m == nil {
			return "", fmt.Errorf("could not parse bug ID from URL: %s", ref)
		}
		return m[1], nil
	}

	// Otherwise treat as a numeric ID.
	for _, c := range ref {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("invalid bug ID: %s", ref)
		}
	}
	return ref, nil
}

func fetchMessages(url string) ([]Message, error) {
	var all []Message
	for url != "" {
		var resp messagesResponse
		if err := getJSON(url, &resp); err != nil {
			return nil, err
		}
		for i := range resp.Entries {
			resp.Entries[i].Author = authorFromLink(resp.Entries[i].OwnerLink)
		}
		all = append(all, resp.Entries...)
		url = resp.NextLink
	}
	return all, nil
}

func fetchAttachmentMetadata(url string) ([]Attachment, error) {
	attachments := make([]Attachment, 0)
	for url != "" {
		var resp attachmentsResponse
		if err := getJSON(url, &resp); err != nil {
			return nil, err
		}
		for _, e := range resp.Entries {
			attachments = append(attachments, Attachment{
				Title:    e.Title,
				Type:     e.Type,
				DataLink: e.DataLink,
			})
		}
		url = resp.NextLink
	}
	return attachments, nil
}

// deduplicateFilename returns a unique filename within dir by appending _1, _2, etc.
// before the file extension if a file with the same name already exists.
func deduplicateFilename(dir, name string) string {
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}

	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// DownloadAttachments downloads each attachment's data to the specified directory
// and sets the FilePath field to the absolute path of the downloaded file.
// Filenames are deduplicated with a _N suffix on collision.
// If a download fails, FilePath is left empty and a warning is printed to stderr.
func DownloadAttachments(attachments []Attachment, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating attachments directory: %w", err)
	}
	for i := range attachments {
		if attachments[i].DataLink == "" {
			continue
		}
		dest := deduplicateFilename(dir, attachments[i].Title)
		if err := downloadFile(attachments[i].DataLink, dest); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to download attachment %q: %v\n", attachments[i].Title, err)
			continue
		}
		absPath, err := filepath.Abs(dest)
		if err != nil {
			absPath = dest
		}
		attachments[i].FilePath = absPath
	}
	return nil
}

// downloadFile fetches a URL and writes the response body to destPath.
// It does not set Accept: application/json, since attachment data endpoints
// redirect to launchpadlibrarian.net which serves raw file content.
func downloadFile(url, destPath string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func authorFromLink(link string) string {
	// owner_link is like "https://api.launchpad.net/devel/~username"
	if i := strings.LastIndex(link, "/~"); i >= 0 {
		return link[i+2:]
	}
	return link
}

func getJSON(url string, v interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	return json.NewDecoder(resp.Body).Decode(v)
}
