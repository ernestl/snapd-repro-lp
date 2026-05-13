package main

import (
	"context"
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
	ubuntuOverride string
)

func resolveModel() {
	if modelName == "" {
		if env := os.Getenv("OPENROUTER_MODEL"); env != "" {
			modelName = env
		} else {
			modelName = "anthropic/claude-sonnet-4"
		}
	}
}

var rootCmd = &cobra.Command{
	Use:   "snapd-repro-lp",
	Short: "Reproduce snapd bugs from Launchpad",
	Long:  "snapd-repro-lp creates reproducers for bugs reported against snapd on Launchpad.",
}

// --- helper: fetch bug and prepare bug directory ---

// fetchAndPrepareBug fetches a bug from Launchpad, creates the bug
// directory, downloads attachments, and writes the bug JSON. It returns
// the bug data and the bug directory path.
func fetchAndPrepareBug(cmd *cobra.Command, bugRef string) (*Bug, string, error) {
	out := cmd.OutOrStdout()

	if verbose {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "verbose: fetching bug %s from Launchpad\n", bugRef)
	}

	bug, err := FetchBug(bugRef)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch bug: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Bug #%d: %s\n", bug.ID, bug.Title)
	_, _ = fmt.Fprintf(out, "URL: %s\n", bug.WebLink)
	_, _ = fmt.Fprintf(out, "Tags: %v\n", bug.Tags)
	_, _ = fmt.Fprintf(out, "Messages: %d\n", len(bug.Messages))
	_, _ = fmt.Fprintf(out, "Attachments: %d\n", len(bug.Attachments))
	if verbose {
		_, _ = fmt.Fprintf(out, "\nDescription:\n%s\n", bug.Description)
		for i, m := range bug.Messages {
			_, _ = fmt.Fprintf(out, "\n--- Message %d (%s) ---\n%s\n", i, m.DateCreated, m.Content)
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
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Directory %s already exists. Overwrite? [y/N]: ", bugDir)
			var answer string
			if _, err := fmt.Fscanln(cmd.InOrStdin(), &answer); err != nil && err.Error() != "unexpected newline" {
				return nil, "", fmt.Errorf("reading answer: %w", err)
			}
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
		if err := DownloadAttachments(bug.Attachments, bugDir); err != nil {
			return nil, "", fmt.Errorf("downloading attachments: %w", err)
		}
		for _, a := range bug.Attachments {
			if a.FilePath != "" {
				_, _ = fmt.Fprintf(out, "Downloaded: %s\n", a.FilePath)
			}
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
	absPath, _ := filepath.Abs(outFile)
	_, _ = fmt.Fprintf(out, "Wrote %s\n", absPath)

	return bug, bugDir, nil
}

// --- helper: run the planning agent ---

func runPlanningAgent(ctx context.Context, cmd *cobra.Command, bug *Bug, bugDir string) (*ReproPlan, error) {
	resolveModel()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
	}

	out := cmd.OutOrStdout()

	// Build planning tools.
	readFile := NewReadFileTool(bugDir)
	reportPlan := NewReportPlanTool()
	executor := NewToolExecutor(readFile, reportPlan)

	// Build LLM client and agent.
	llmClient := NewLLMClient(apiKey, modelName)
	agent := NewAgent(llmClient, executor, AgentConfig{
		MaxIterations: maxIterations,
		Verbose:       verbose,
		Output:        cmd.ErrOrStderr(),
	})

	// Build prompts.
	systemPrompt := BuildPlanningPrompt(bug)
	userMessage := BuildPlanningUserMessage(bug)

	_, _ = fmt.Fprintf(out, "\nPlanning reproduction (model: %s)...\n", modelName)
	result, err := agent.Run(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, fmt.Errorf("planning agent failed: %w", err)
	}

	// The agent returned via report_plan → reportPlan.Plan is set.
	if reportPlan.Plan != nil {
		plan := reportPlan.Plan
		plan.BugID = bug.ID
		plan.Title = bug.Title
		plan.ModelUsed = modelName

		_, _ = fmt.Fprintf(out, "\nToken usage: %d prompt + %d completion = %d total\n",
			agent.TotalUsage.PromptTokens,
			agent.TotalUsage.CompletionTokens,
			agent.TotalUsage.TotalTokens)

		return plan, nil
	}

	// Fallback: agent stopped without calling report_plan.
	return nil, fmt.Errorf("planning agent did not produce a plan. Last output: %s", result.LastMessage)
}

// --- helper: run the execution agent ---

func runExecutionAgent(ctx context.Context, cmd *cobra.Command, plan *ReproPlan, bugDir string) (*ReproResult, *Usage, error) {
	resolveModel()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, nil, fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
	}

	out := cmd.OutOrStdout()

	// Determine Ubuntu version: override flag takes precedence.
	ubuntuVersion := plan.UbuntuVersion
	if ubuntuOverride != "" {
		ubuntuVersion = ubuntuOverride
		_, _ = fmt.Fprintf(out, "Overriding Ubuntu version: %s → %s\n", plan.UbuntuVersion, ubuntuOverride)
	}

	// Create and launch LXD container.
	container := NewLXDManager()
	_, _ = fmt.Fprintf(out, "Launching container %s (ubuntu:%s)...\n", container.Name(), ubuntuVersion)
	if err := container.Launch(ubuntuVersion); err != nil {
		return nil, nil, fmt.Errorf("launching container: %w", err)
	}
	defer func() {
		_, _ = fmt.Fprintf(out, "Cleaning up container %s...\n", container.Name())
		if delErr := container.Delete(); delErr != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to delete container: %v\n", delErr)
		}
	}()

	// Build execution tools.
	runCmd := NewRunCommandTool(container)
	reportResult := NewReportResultTool()
	executor := NewToolExecutor(runCmd, reportResult)

	// Build LLM client and agent.
	llmClient := NewLLMClient(apiKey, modelName)
	agent := NewAgent(llmClient, executor, AgentConfig{
		MaxIterations: maxIterations,
		Verbose:       verbose,
		Output:        cmd.ErrOrStderr(),
	})

	// Build prompts.
	systemPrompt := BuildExecutionPrompt(plan, container.Name())
	userMessage := BuildExecutionUserMessage(plan)

	_, _ = fmt.Fprintf(out, "Executing plan (model: %s, max iterations: %d)...\n", modelName, maxIterations)
	result, err := agent.Run(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, nil, fmt.Errorf("execution agent failed: %w", err)
	}

	// Extract the structured result from the report_result tool.
	if result.StoppedByTool == "report_result" && reportResult.Result != nil {
		return reportResult.Result, &agent.TotalUsage, nil
	}

	// Fallback: agent stopped without calling report_result.
	fallback := &ReproResult{
		Reproduced:  false,
		Explanation: fmt.Sprintf("Agent stopped without reporting a result. Last output: %s", result.LastMessage),
	}
	return fallback, &agent.TotalUsage, nil
}

// --- helper: print and save execution result ---

func saveExecutionResult(cmd *cobra.Command, result *ReproResult, usage *Usage, bugDir string) {
	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "\n=== Reproduction Result ===\n")
	if result.Reproduced {
		_, _ = fmt.Fprintf(out, "Status: REPRODUCED\n")
	} else {
		_, _ = fmt.Fprintf(out, "Status: NOT REPRODUCED\n")
	}
	_, _ = fmt.Fprintf(out, "\nExplanation:\n%s\n", result.Explanation)

	if result.Script != "" {
		_, _ = fmt.Fprintf(out, "\nScript:\n%s\n", result.Script)

		scriptFile := filepath.Join(bugDir, "reproducer.sh")
		if err := os.WriteFile(scriptFile, []byte(result.Script), 0755); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to write script: %v\n", err)
		} else {
			_, _ = fmt.Fprintf(out, "\nScript saved to %s\n", scriptFile)
		}
	}

	resultFile := filepath.Join(bugDir, "result.json")
	resultData, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(resultFile, resultData, 0644); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to write result: %v\n", err)
	} else {
		_, _ = fmt.Fprintf(out, "Result saved to %s\n", resultFile)
	}

	if usage != nil {
		_, _ = fmt.Fprintf(out, "\nToken usage: %d prompt + %d completion = %d total\n",
			usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
}

// --- plan command ---

var planCmd = &cobra.Command{
	Use:   "plan [bug-ref]",
	Short: "Analyze a bug and produce a reproduction plan",
	Long: `Fetch a Launchpad bug report, analyze it with an LLM, and produce a structured
reproduction plan (plan.json). The plan includes the Ubuntu version to use,
step-by-step commands, and expected results.

Requires OPENROUTER_API_KEY to be set.`,
	Example: "  snapd-repro-lp plan 1662786",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bug, bugDir, err := fetchAndPrepareBug(cmd, args[0])
		if err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		plan, err := runPlanningAgent(ctx, cmd, bug, bugDir)
		if err != nil {
			return err
		}

		// Save plan.
		planFile := filepath.Join(bugDir, "plan.json")
		if err := SavePlan(plan, planFile); err != nil {
			return fmt.Errorf("saving plan: %w", err)
		}

		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "\n=== Reproduction Plan ===\n")
		_, _ = fmt.Fprintf(out, "Ubuntu version: %s\n", plan.UbuntuVersion)
		_, _ = fmt.Fprintf(out, "Steps: %d\n", len(plan.Steps))
		for i, step := range plan.Steps {
			_, _ = fmt.Fprintf(out, "  %d. %s\n", i+1, step.Description)
			_, _ = fmt.Fprintf(out, "     $ %s\n", step.Command)
		}
		_, _ = fmt.Fprintf(out, "Expected: %s\n", plan.ExpectedResult)
		_, _ = fmt.Fprintf(out, "\nPlan saved to %s\n", planFile)
		_, _ = fmt.Fprintf(out, "\nRun the plan with:\n")
		_, _ = fmt.Fprintf(out, "  snapd-repro-lp exec %s\n", planFile)

		return nil
	},
}

// --- exec command ---

var execCmd = &cobra.Command{
	Use:   "exec [plan-file]",
	Short: "Execute a reproduction plan in an LXD container",
	Long: `Read a plan.json file produced by the plan command, launch an LXD container
with the specified Ubuntu version, and execute the reproduction steps.

Requires OPENROUTER_API_KEY to be set.`,
	Example: "  snapd-repro-lp exec bug-1662786/plan.json",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		planFile := args[0]
		plan, err := LoadPlan(planFile)
		if err != nil {
			return fmt.Errorf("loading plan: %w", err)
		}

		bugDir := filepath.Dir(planFile)

		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Loaded plan for bug #%d: %s\n", plan.BugID, plan.Title)
		_, _ = fmt.Fprintf(out, "Ubuntu version: %s\n", plan.UbuntuVersion)
		_, _ = fmt.Fprintf(out, "Steps: %d\n", len(plan.Steps))

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		result, usage, err := runExecutionAgent(ctx, cmd, plan, bugDir)
		if err != nil {
			return err
		}

		saveExecutionResult(cmd, result, usage, bugDir)
		return nil
	},
}

// --- reproduce command (convenience: plan + exec) ---

var reproduceCmd = &cobra.Command{
	Use:   "reproduce [bug-ref]",
	Short: "Plan and execute a bug reproducer in one step",
	Long: `Fetch a Launchpad bug report, analyze it to produce a reproduction plan,
then execute the plan in an LXD container. This combines the plan and exec
commands into a single step.

Requires OPENROUTER_API_KEY to be set.`,
	Example: "  snapd-repro-lp reproduce 1662786",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bug, bugDir, err := fetchAndPrepareBug(cmd, args[0])
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

		// Phase 1: Plan.
		plan, err := runPlanningAgent(ctx, cmd, bug, bugDir)
		if err != nil {
			return err
		}

		// Save plan.
		planFile := filepath.Join(bugDir, "plan.json")
		if err := SavePlan(plan, planFile); err != nil {
			return fmt.Errorf("saving plan: %w", err)
		}

		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "\n=== Reproduction Plan ===\n")
		_, _ = fmt.Fprintf(out, "Ubuntu version: %s\n", plan.UbuntuVersion)
		for i, step := range plan.Steps {
			_, _ = fmt.Fprintf(out, "  %d. %s\n", i+1, step.Description)
		}
		_, _ = fmt.Fprintf(out, "Plan saved to %s\n", planFile)

		// Phase 2: Execute.
		_, _ = fmt.Fprintf(out, "\n--- Executing plan ---\n")
		result, usage, err := runExecutionAgent(ctx, cmd, plan, bugDir)
		if err != nil {
			return err
		}

		saveExecutionResult(cmd, result, usage, bugDir)
		return nil
	},
}

// --- test command tree ---

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test individual components",
	Long:  "Subcommands for manually testing LLM, LXD, and other components.",
}

var testChatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "Send a message to the LLM and print the response",
	Long:  "Quick smoke test for the OpenRouter LLM integration. Requires OPENROUTER_API_KEY.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resolveModel()

		apiKey := os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			return fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
		}

		llmClient := NewLLMClient(apiKey, modelName)
		messages := []ChatMessage{
			TextMessage(RoleUser, args[0]),
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Sending to %s...\n", modelName)

		resp, err := llmClient.ChatCompletion(ctx, messages, nil)
		if err != nil {
			return fmt.Errorf("LLM request failed: %w", err)
		}

		if len(resp.Choices) == 0 {
			return fmt.Errorf("no choices in response")
		}

		msg := resp.Choices[0].Message
		if msg.Content != nil {
			_, _ = fmt.Fprintln(out, *msg.Content)
		} else {
			_, _ = fmt.Fprintln(out, "(no text content in response)")
		}

		if resp.Usage != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n[tokens: %d prompt + %d completion = %d total]\n",
				resp.Usage.PromptTokens,
				resp.Usage.CompletionTokens,
				resp.Usage.TotalTokens)
		}

		return nil
	},
}

var testLxdCmd = &cobra.Command{
	Use:   "lxd",
	Short: "Test LXD container operations",
	Long:  "Subcommands for manually testing LXD container launch, exec, and delete.",
}

var testLxdLaunchCmd = &cobra.Command{
	Use:     "launch [version]",
	Short:   "Launch an LXD container",
	Long:    "Launch an Ubuntu LXD container and print its name. Use the name with exec and delete.",
	Example: "  snapd-repro-lp test lxd launch 24.04",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		version := args[0]
		container := NewLXDManager()
		out := cmd.OutOrStdout()

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Launching container %s (ubuntu:%s)...\n", container.Name(), version)
		if err := container.Launch(version); err != nil {
			return fmt.Errorf("launch failed: %w", err)
		}

		_, _ = fmt.Fprintln(out, container.Name())
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Container is ready. Run commands with:\n")
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  snapd-repro-lp test lxd exec %s \"snap version\"\n", container.Name())
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Delete with:\n")
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  snapd-repro-lp test lxd delete %s\n", container.Name())
		return nil
	},
}

var testLxdExecCmd = &cobra.Command{
	Use:     "exec [container] [command]",
	Short:   "Execute a command in an LXD container",
	Long:    "Run a shell command inside an existing LXD container and print the output.",
	Example: "  snapd-repro-lp test lxd exec snapd-repro-abc123 \"snap version\"",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		containerName := args[0]
		command := args[1]

		container := NewLXDManagerFromName(containerName)
		out := cmd.OutOrStdout()

		if verbose {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Executing in %s: %s\n", containerName, command)
		}

		result, err := container.Exec(command)
		if err != nil {
			return fmt.Errorf("exec failed: %w", err)
		}

		_, _ = fmt.Fprint(out, result.Output)
		if result.ExitCode != 0 {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[exit code: %d]\n", result.ExitCode)
		}
		return nil
	},
}

var testLxdDeleteCmd = &cobra.Command{
	Use:     "delete [container]",
	Short:   "Delete an LXD container",
	Long:    "Force-delete an existing LXD container.",
	Example: "  snapd-repro-lp test lxd delete snapd-repro-abc123",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		containerName := args[0]
		container := NewLXDManagerFromName(containerName)

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Deleting container %s...\n", containerName)
		if err := container.Delete(); err != nil {
			return fmt.Errorf("delete failed: %w", err)
		}

		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Container %s deleted.\n", containerName)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().StringVar(&modelName, "model", "", "LLM model to use via OpenRouter (env: OPENROUTER_MODEL)")
	rootCmd.PersistentFlags().IntVar(&maxIterations, "max-iter", 60, "maximum agent iterations")

	// plan command flags.
	planCmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "directory to write output (default: current directory)")
	planCmd.Flags().BoolVarP(&forceOverwrite, "force", "f", false, "overwrite existing bug directory without prompting")

	// exec command flags.
	execCmd.Flags().StringVar(&ubuntuOverride, "ubuntu", "", "override the Ubuntu version from the plan (e.g. 22.04)")

	// reproduce command flags (combines plan + exec).
	reproduceCmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "directory to write output (default: current directory)")
	reproduceCmd.Flags().BoolVarP(&forceOverwrite, "force", "f", false, "overwrite existing bug directory without prompting")
	reproduceCmd.Flags().StringVar(&ubuntuOverride, "ubuntu", "", "override the Ubuntu version from the plan (e.g. 22.04)")

	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(reproduceCmd)

	testLxdCmd.AddCommand(testLxdLaunchCmd)
	testLxdCmd.AddCommand(testLxdExecCmd)
	testLxdCmd.AddCommand(testLxdDeleteCmd)
	testCmd.AddCommand(testChatCmd)
	testCmd.AddCommand(testLxdCmd)
	rootCmd.AddCommand(testCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
