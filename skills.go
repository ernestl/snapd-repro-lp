package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

// Skill represents a single skill entry in the skill index.
type Skill struct {
	Name        string   `json:"name"`
	Keywords    []string `json:"keywords"`
	Summary     string   `json:"summary"`
	ContentFile string   `json:"content_file"`
}

// skillsFile is the top-level structure of skills.json.
type skillsFile struct {
	Skills []Skill `json:"skills"`
}

// SkillIndex holds parsed skills and provides lookup methods.
type SkillIndex struct {
	skills map[string]*Skill
	order  []string // preserve insertion order for listing
	fsys   fs.FS    // for reading content files
}

// NewSkillIndex parses a skills.json byte slice and stores the given
// filesystem for reading content files. The fs.FS should be rooted at
// the project directory so that content_file paths resolve correctly.
func NewSkillIndex(data []byte, fsys fs.FS) (*SkillIndex, error) {
	var sf skillsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parsing skills index: %w", err)
	}

	idx := &SkillIndex{
		skills: make(map[string]*Skill, len(sf.Skills)),
		order:  make([]string, 0, len(sf.Skills)),
		fsys:   fsys,
	}
	for i := range sf.Skills {
		s := &sf.Skills[i]
		idx.skills[s.Name] = s
		idx.order = append(idx.order, s.Name)
	}
	return idx, nil
}

// List returns a compact multi-line listing of all skills with their
// keywords, suitable for injection into a system prompt.
func (idx *SkillIndex) List() string {
	var b strings.Builder
	for _, name := range idx.order {
		s := idx.skills[name]
		b.WriteString(fmt.Sprintf("- %s [%s]\n", s.Name, strings.Join(s.Keywords, ", ")))
	}
	return b.String()
}

// Describe returns the summary for the named skill. If the skill is
// not found, it returns an error message listing available skills.
func (idx *SkillIndex) Describe(name string) (string, error) {
	s, ok := idx.skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q; available skills: %s", name, strings.Join(idx.order, ", "))
	}
	return s.Summary, nil
}

// Load reads and returns the full content of the named skill's
// content file from the embedded filesystem.
func (idx *SkillIndex) Load(name string) (string, error) {
	s, ok := idx.skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q; available skills: %s", name, strings.Join(idx.order, ", "))
	}

	data, err := fs.ReadFile(idx.fsys, s.ContentFile)
	if err != nil {
		return "", fmt.Errorf("reading skill content file %q: %w", s.ContentFile, err)
	}

	content := string(data)
	const maxContent = 50000
	if len(content) > maxContent {
		content = content[:maxContent] + "\n...[truncated]"
	}
	return content, nil
}

// --- describe_skill tool ---

// DescribeSkillTool allows the LLM to get a short summary of a skill
// to decide whether to load the full content.
type DescribeSkillTool struct {
	index *SkillIndex
}

// NewDescribeSkillTool creates a new describe_skill tool.
func NewDescribeSkillTool(index *SkillIndex) *DescribeSkillTool {
	return &DescribeSkillTool{index: index}
}

func (t *DescribeSkillTool) Name() string { return "describe_skill" }

func (t *DescribeSkillTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "describe_skill",
			Description: "Get a short description of a debugging skill to decide if it is worth loading in full. Use this before load_skill to check relevance.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The skill name (e.g. 'snap-refresh', 'journalctl').",
					},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
	}
}

type describeSkillArgs struct {
	Name string `json:"name"`
}

func (t *DescribeSkillTool) Execute(_ context.Context, argsJSON string) (*ToolResult, error) {
	var args describeSkillArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing describe_skill args: %w", err)
	}
	if args.Name == "" {
		return &ToolResult{Output: "error: name is required"}, nil
	}

	summary, err := t.index.Describe(args.Name)
	if err != nil {
		return &ToolResult{Output: fmt.Sprintf("error: %v", err)}, nil
	}

	return &ToolResult{Output: summary, Summary: args.Name}, nil
}

// --- load_skill tool ---

// LoadSkillTool allows the LLM to load the full content of a
// debugging skill, including detailed commands and workflows.
type LoadSkillTool struct {
	index *SkillIndex
}

// NewLoadSkillTool creates a new load_skill tool.
func NewLoadSkillTool(index *SkillIndex) *LoadSkillTool {
	return &LoadSkillTool{index: index}
}

func (t *LoadSkillTool) Name() string { return "load_skill" }

func (t *LoadSkillTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "load_skill",
			Description: "Load the full content of a debugging skill, including detailed commands, workflows, and troubleshooting steps. Use describe_skill first to check relevance.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The skill name (e.g. 'snap-refresh', 'journalctl').",
					},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
	}
}

type loadSkillArgs struct {
	Name string `json:"name"`
}

func (t *LoadSkillTool) Execute(_ context.Context, argsJSON string) (*ToolResult, error) {
	var args loadSkillArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing load_skill args: %w", err)
	}
	if args.Name == "" {
		return &ToolResult{Output: "error: name is required"}, nil
	}

	content, err := t.index.Load(args.Name)
	if err != nil {
		return &ToolResult{Output: fmt.Sprintf("error: %v", err)}, nil
	}

	return &ToolResult{Output: content, Summary: args.Name}, nil
}
