package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	verbose        bool
	outputDir      string
	forceOverwrite bool
	modelName      string
	maxIterations  int
)

//go:embed skills.json
var skillsJSON []byte

//go:embed skills/*
var skillsFS embed.FS

//go:embed snapd_revision_version_map.txt
var revisionMapData string

// skillIndex is the parsed skill index, initialized at startup.
var skillIndex *SkillIndex

// revisionMap is the parsed snapd revision-version mapping,
// initialized at startup.
var revisionMap []SnapdRevision

func init() {
	var err error
	skillIndex, err = NewSkillIndex(skillsJSON, skillsFS)
	if err != nil {
		panic(fmt.Sprintf("failed to load embedded skills: %v", err))
	}

	revisionMap, err = parseRevisionMap(revisionMapData)
	if err != nil {
		panic(fmt.Sprintf("failed to parse embedded revision map: %v", err))
	}
}

const defaultUbuntuVersion = "24.04"

func resolveModel() {
	if modelName == "" {
		if env := os.Getenv("OPENROUTER_MODEL"); env != "" {
			modelName = env
		} else {
			modelName = "deepseek/deepseek-v4-pro"
		}
	}
}

// launchVM creates and launches an LXD VM with the given Ubuntu version.
// It prints progress and returns the manager. The caller must call
// instance.Delete() when done.
func launchVM(cmd *cobra.Command, ubuntuVersion string, step, totalSteps int) (*LXDManager, error) {
	out := cmd.OutOrStdout()
	instance := NewLXDManager()
	_, _ = fmt.Fprintf(out, "\n%s Launching VM %s (ubuntu:%s)...\n",
		boldCyan(fmt.Sprintf("Step %d/%d:", step, totalSteps)), instance.Name(), ubuntuVersion)
	status, err := instance.LaunchCached(ubuntuVersion, "vm")
	if err != nil {
		return nil, fmt.Errorf("launching VM: %w", err)
	}
	switch status {
	case CacheHit:
		_, _ = fmt.Fprintf(out, "  (from cached snapshot)\n")
	case CacheMiss:
		_, _ = fmt.Fprintf(out, "  (created cache for ubuntu:%s)\n", ubuntuVersion)
	case CacheFallback:
		_, _ = fmt.Fprintf(out, "  (fresh launch, cache unavailable)\n")
	}
	return instance, nil
}

// cleanupVM deletes an LXD instance, printing progress.
func cleanupVM(cmd *cobra.Command, instance *LXDManager) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Cleaning up VM %s...\n", instance.Name())
	if err := instance.Delete(); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", yellow(fmt.Sprintf("warning: failed to delete instance: %v", err)))
	}
}

var rootCmd = &cobra.Command{
	Use:          "snapd-repro-lp",
	Short:        "Reproduce snapd bugs from Launchpad",
	Long:         "snapd-repro-lp creates reproducers for bugs reported against snapd on Launchpad.",
	SilenceUsage: true,
}

// --- helper: fetch bug and prepare bug directory ---

// fetchAndPrepareBug fetches a bug from Launchpad, creates the bug
// directory, downloads attachments, and writes the bug JSON. It returns
// the bug data and the bug directory path. The step and totalSteps
// parameters control the "Step X/Y:" header.
func fetchAndPrepareBug(cmd *cobra.Command, bugRef string, step, totalSteps int) (*Bug, string, error) {
	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "%s Fetching bug #%s...\n", boldCyan(fmt.Sprintf("Step %d/%d:", step, totalSteps)), bugRef)

	if verbose {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  verbose: fetching bug %s from Launchpad\n", bugRef)
	}

	bug, err := FetchBug(bugRef)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch bug: %w", err)
	}

	_, _ = fmt.Fprintf(out, "  Description: %s\n", bug.Title)
	_, _ = fmt.Fprintf(out, "  Link: %s\n", bug.WebLink)
	_, _ = fmt.Fprintf(out, "  Tags: %v\n", bug.Tags)
	_, _ = fmt.Fprintf(out, "  Messages: %d, Attachments: %d\n", len(bug.Messages), len(bug.Attachments))
	if verbose {
		_, _ = fmt.Fprintf(out, "\n  Description:\n%s\n", bug.Description)
		for i, m := range bug.Messages {
			_, _ = fmt.Fprintf(out, "\n  --- Message %d (%s) ---\n%s\n", i, m.DateCreated, m.Content)
		}
	}

	// Determine the base directory for output.
	baseDir := outputDir
	if baseDir == "" {
		baseDir = "."
	}

	// Each bug gets its own subdirectory.
	bugDir := filepath.Join(baseDir, fmt.Sprintf("bug-%d", bug.ID))

	// Check if the directory already exists.
	if info, statErr := os.Stat(bugDir); statErr == nil && info.IsDir() {
		if !forceOverwrite {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n  %s", yellow(fmt.Sprintf("Directory %s already exists. Overwrite? [y/N]: ", bugDir)))
			var answer string
			if _, err := fmt.Fscanln(cmd.InOrStdin(), &answer); err != nil && err.Error() != "unexpected newline" {
				return nil, "", fmt.Errorf("reading answer: %w", err)
			}
			_, _ = fmt.Fprintln(out)
			if answer != "y" && answer != "Y" {
				return nil, "", fmt.Errorf("aborted: directory %s already exists", bugDir)
			}
		}
		if err := os.RemoveAll(bugDir); err != nil {
			return nil, "", fmt.Errorf("removing existing directory: %w", err)
		}
	}

	if err := os.MkdirAll(bugDir, 0755); err != nil {
		return nil, "", fmt.Errorf("creating bug directory: %w", err)
	}

	// Download attachments into the bug directory.
	if len(bug.Attachments) > 0 {
		attachmentsDir := filepath.Join(bugDir, "attachments")
		if err := DownloadAttachments(bug.Attachments, attachmentsDir); err != nil {
			return nil, "", fmt.Errorf("downloading attachments: %w", err)
		}
	}

	// Write JSON summary file.
	outFile := filepath.Join(bugDir, fmt.Sprintf("bug-%d.json", bug.ID))
	data, err := json.MarshalIndent(bug, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("marshalling bug to JSON: %w", err)
	}
	if err := os.WriteFile(outFile, data, 0644); err != nil {
		return nil, "", fmt.Errorf("writing %s: %w", outFile, err)
	}
	_, _ = fmt.Fprintf(out, "  Saved bug data to %s\n", blue(fileHyperlink(outFile)))

	return bug, bugDir, nil
}

// --- helper: run the single-phase reproduce agent ---

// runReproduceAgent runs the single-phase agent that analyzes the bug and
// attempts reproduction in one session. The instanceRef is shared with the
// RelaunchVMTool so the LLM can swap the VM mid-conversation.
func runReproduceAgent(ctx context.Context, cmd *cobra.Command, bug *Bug, bugDir string, instanceRef *InstanceRef) (*ReproResult, *Usage, string, error) {
	resolveModel()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, nil, "", fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
	}

	out := cmd.OutOrStdout()

	// Build tools with a shared instance ref.
	attachmentsDir := filepath.Join(bugDir, "attachments")
	var readFile *ReadFileTool
	if len(bug.Attachments) > 0 {
		readFile = NewReadFileTool(attachmentsDir)
	}
	runCmd := NewRunCommandToolFromRef(instanceRef)
	reportResult := NewReportResultTool()
	describeSkill := NewDescribeSkillTool(skillIndex)
	loadSkill := NewLoadSkillTool(skillIndex)
	queryRevisions := NewQueryRevisionsTool(revisionMap)
	relaunchVM := NewRelaunchVMTool(instanceRef, func() InstanceManager { return NewLXDManager() }, cmd.ErrOrStderr())

	tools := []Tool{runCmd, reportResult, describeSkill, loadSkill, queryRevisions, relaunchVM}
	if readFile != nil {
		tools = append(tools, readFile)
	}
	executor := NewToolExecutor(tools...)

	// Build LLM client and agent.
	llmClient := NewLLMClient(apiKey, modelName)
	agent := NewAgent(llmClient, executor, AgentConfig{
		MaxIterations: maxIterations,
		Verbose:       verbose,
		Output:        cmd.ErrOrStderr(),
		Prefix:        "  ",
	})

	// Build prompts.
	systemPrompt := BuildReproducePrompt(bug, instanceRef.Instance.Name(), skillIndex)
	userMessage := BuildReproduceUserMessage(bug)

	// Save prompt for inspection.
	promptFile := filepath.Join(bugDir, "reproduce-prompt.html")
	if err := SavePromptHTML(promptFile, fmt.Sprintf("Reproduce Prompt — Bug #%d", bug.ID), systemPrompt, userMessage); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", yellow(fmt.Sprintf("warning: failed to save prompt HTML: %v", err)))
	} else {
		_, _ = fmt.Fprintf(out, "  Generated reproduce prompt: %s\n", blue(fileHyperlink(promptFile)))
	}

	result, err := agent.Run(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, nil, agent.Log(), fmt.Errorf("reproduce agent failed: %w", err)
	}

	// Extract the structured result from the report_result tool.
	if result.StoppedByTool == "report_result" && reportResult.Result != nil {
		return reportResult.Result, &agent.TotalUsage, agent.Log(), nil
	}

	// Fallback: agent stopped without calling report_result.
	var explanation string
	switch {
	case result.MaxIterationsReached:
		explanation = fmt.Sprintf(
			"Agent exhausted its iteration budget (%d) without reaching a conclusion.\n\nRecent activity:\n%s",
			maxIterations, result.RecentActivity)
	case result.LastMessage != "":
		explanation = fmt.Sprintf(
			"Agent stopped without reporting a result. Last output: %s",
			result.LastMessage)
	default:
		explanation = "Agent stopped without reporting a result (no output captured)."
	}
	fallback := &ReproResult{
		Reproduced:  false,
		Explanation: explanation,
	}
	return fallback, &agent.TotalUsage, agent.Log(), nil
}

// --- helper: print and save result ---

func saveExecutionResult(cmd *cobra.Command, result *ReproResult, bugDir string) {
	out := cmd.OutOrStdout()

	if result.Reproduced {
		_, _ = fmt.Fprintf(out, "\n  Status: %s\n", bold(green("REPRODUCED")))
	} else {
		_, _ = fmt.Fprintf(out, "\n  Status: %s\n", bold(red("NOT REPRODUCED")))
	}
	_, _ = fmt.Fprintf(out, "  Explanation: %s\n", result.Explanation)

	if result.Script != "" {
		scriptFile := filepath.Join(bugDir, "reproducer.sh")
		if err := os.WriteFile(scriptFile, []byte(result.Script), 0755); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", yellow(fmt.Sprintf("warning: failed to write script: %v", err)))
		} else {
			_, _ = fmt.Fprintf(out, "  Saved reproducer script to %s\n", blue(fileHyperlink(scriptFile)))
		}
	}

	resultFile := filepath.Join(bugDir, "result.json")
	resultData, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(resultFile, resultData, 0644); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", yellow(fmt.Sprintf("warning: failed to write result: %v", err)))
	} else {
		_, _ = fmt.Fprintf(out, "  Saved result to %s\n", blue(fileHyperlink(resultFile)))
	}
}

// saveAgentLog writes an agent interaction log to the given file and
// prints a link. If the log is empty or writing fails, it warns.
func saveAgentLog(cmd *cobra.Command, log, path string) {
	if log == "" {
		return
	}
	out := cmd.OutOrStdout()
	if err := os.WriteFile(path, []byte(log), 0644); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", yellow(fmt.Sprintf("warning: failed to write log: %v", err)))
	} else {
		_, _ = fmt.Fprintf(out, "  Saved agent log to %s\n", blue(fileHyperlink(path)))
	}
}

// --- reproduce command ---

var reproduceCmd = &cobra.Command{
	Use:   "reproduce [bug-ref]",
	Short: "Analyze and reproduce a bug in one step",
	Long: `Fetch a Launchpad bug report, analyze it with an LLM, and attempt to reproduce
the bug inside an LXD VM — all in a single agent session. The LLM receives the
full bug context and can investigate, determine the correct Ubuntu version
(relaunching the VM if needed), and execute reproduction steps.

Requires OPENROUTER_API_KEY to be set.`,
	Example: "  snapd-repro-lp reproduce 1662786",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bug, bugDir, err := fetchAndPrepareBug(cmd, args[0], 1, 3)
		if err != nil {
			return err
		}

		apiKey := os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nSet OPENROUTER_API_KEY to enable AI-assisted reproduction.\n")
			return nil
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		out := cmd.OutOrStdout()

		// Launch default VM.
		instance, err := launchVM(cmd, defaultUbuntuVersion, 2, 3)
		if err != nil {
			return err
		}
		// Use a shared InstanceRef so the LLM can relaunch the VM.
		instanceRef := &InstanceRef{Instance: instance}
		defer func() {
			cleanupVM(cmd, instanceRef.Instance.(*LXDManager))
		}()

		// Run the single-phase agent.
		resolveModel()
		_, _ = fmt.Fprintf(out, "\n%s Reproducing bug (model: %s)...\n", boldCyan("Step 3/3:"), modelName)

		result, usage, agentLog, err := runReproduceAgent(ctx, cmd, bug, bugDir, instanceRef)
		saveAgentLog(cmd, agentLog, filepath.Join(bugDir, "reproduce-log.txt"))
		if err != nil {
			return err
		}

		saveExecutionResult(cmd, result, bugDir)

		if usage != nil {
			_, _ = fmt.Fprintf(out, "\n%s\n",
				dim(fmt.Sprintf("Token usage: %d prompt + %d completion = %d total",
					usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)))
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().StringVar(&modelName, "model", "", "LLM model to use via OpenRouter (env: OPENROUTER_MODEL)")
	rootCmd.PersistentFlags().IntVar(&maxIterations, "max-iter", 60, "maximum agent iterations")

	// reproduce command flags.
	reproduceCmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "directory to write output (default: current directory)")
	reproduceCmd.Flags().BoolVarP(&forceOverwrite, "force", "f", false, "overwrite existing bug directory without prompting")

	rootCmd.AddCommand(reproduceCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
