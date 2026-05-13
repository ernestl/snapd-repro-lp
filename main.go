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
	ubuntuOverride string
)

//go:embed skills.json
var skillsJSON []byte

//go:embed skills/*
var skillsFS embed.FS

// skillIndex is the parsed skill index, initialized at startup.
var skillIndex *SkillIndex

func init() {
	var err error
	skillIndex, err = NewSkillIndex(skillsJSON, skillsFS)
	if err != nil {
		panic(fmt.Sprintf("failed to load embedded skills: %v", err))
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
	if err := instance.Launch(ubuntuVersion, "vm"); err != nil {
		return nil, fmt.Errorf("launching VM: %w", err)
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

// --- helper: run the planning agent ---

func runPlanningAgent(ctx context.Context, cmd *cobra.Command, bug *Bug, bugDir string, instance InstanceManager) (*ReproPlan, *Usage, string, error) {
	resolveModel()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, nil, "", fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
	}

	out := cmd.OutOrStdout()

	// Build planning tools.
	attachmentsDir := filepath.Join(bugDir, "attachments")
	var readFile *ReadFileTool
	if len(bug.Attachments) > 0 {
		readFile = NewReadFileTool(attachmentsDir)
	}
	runCmd := NewRunCommandTool(instance)
	reportPlan := NewReportPlanTool()
	describeSkill := NewDescribeSkillTool(skillIndex)
	loadSkill := NewLoadSkillTool(skillIndex)
	tools := []Tool{runCmd, reportPlan, describeSkill, loadSkill}
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
	systemPrompt := BuildPlanningPrompt(bug, instance.Name(), skillIndex)
	userMessage := BuildPlanningUserMessage(bug)

	// Save prompt for inspection.
	promptFile := filepath.Join(bugDir, "planning-prompt.html")
	if err := SavePromptHTML(promptFile, fmt.Sprintf("Planning Prompt — Bug #%d", bug.ID), systemPrompt, userMessage); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", yellow(fmt.Sprintf("warning: failed to save prompt HTML: %v", err)))
	} else {
		_, _ = fmt.Fprintf(out, "  Generated planning prompt: %s\n", blue(fileHyperlink(promptFile)))
	}

	result, err := agent.Run(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, nil, agent.Log(), fmt.Errorf("planning agent failed: %w", err)
	}

	// The agent returned via report_plan → reportPlan.Plan is set.
	if reportPlan.Plan != nil {
		plan := reportPlan.Plan
		plan.BugID = bug.ID
		plan.Title = bug.Title
		plan.ModelUsed = modelName

		return plan, &agent.TotalUsage, agent.Log(), nil
	}

	// Fallback: agent stopped without calling report_plan.
	return nil, nil, agent.Log(), fmt.Errorf("planning agent did not produce a plan. Last output: %s", result.LastMessage)
}

// --- helper: run the execution agent ---

func runExecutionAgent(ctx context.Context, cmd *cobra.Command, plan *ReproPlan, bugDir string, instance InstanceManager) (*ReproResult, *Usage, string, error) {
	resolveModel()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, nil, "", fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
	}

	out := cmd.OutOrStdout()

	// Build execution tools.
	runCmd := NewRunCommandTool(instance)
	reportResult := NewReportResultTool()
	describeSkill := NewDescribeSkillTool(skillIndex)
	loadSkill := NewLoadSkillTool(skillIndex)
	executor := NewToolExecutor(runCmd, reportResult, describeSkill, loadSkill)

	// Build LLM client and agent.
	llmClient := NewLLMClient(apiKey, modelName)
	agent := NewAgent(llmClient, executor, AgentConfig{
		MaxIterations: maxIterations,
		Verbose:       verbose,
		Output:        cmd.ErrOrStderr(),
		Prefix:        "  ",
	})

	// Build prompts.
	systemPrompt := BuildExecutionPrompt(plan, instance.Name(), skillIndex)
	userMessage := BuildExecutionUserMessage(plan)

	// Save prompt for inspection.
	promptFile := filepath.Join(bugDir, "execution-prompt.html")
	if err := SavePromptHTML(promptFile, fmt.Sprintf("Execution Prompt — Bug #%d", plan.BugID), systemPrompt, userMessage); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", yellow(fmt.Sprintf("warning: failed to save prompt HTML: %v", err)))
	} else {
		_, _ = fmt.Fprintf(out, "  Generated execution prompt: %s\n", blue(fileHyperlink(promptFile)))
	}

	result, err := agent.Run(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, nil, agent.Log(), fmt.Errorf("execution agent failed: %w", err)
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

// --- helper: print and save execution result ---

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
		bug, bugDir, err := fetchAndPrepareBug(cmd, args[0], 1, 3)
		if err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		out := cmd.OutOrStdout()

		// Launch a default VM for investigation during planning.
		instance, err := launchVM(cmd, defaultUbuntuVersion, 2, 3)
		if err != nil {
			return err
		}
		defer cleanupVM(cmd, instance)

		resolveModel()
		_, _ = fmt.Fprintf(out, "\n%s Planning reproduction (model: %s)...\n", boldCyan("Step 3/3:"), modelName)

		plan, planUsage, planLog, err := runPlanningAgent(ctx, cmd, bug, bugDir, instance)
		saveAgentLog(cmd, planLog, filepath.Join(bugDir, "planning-log.txt"))
		if err != nil {
			return err
		}

		// Save plan.
		planFile := filepath.Join(bugDir, "plan.json")
		if err := SavePlan(plan, planFile); err != nil {
			return fmt.Errorf("saving plan: %w", err)
		}
		_, _ = fmt.Fprintf(out, "  Saved plan to %s\n", blue(fileHyperlink(planFile)))

		_, _ = fmt.Fprintf(out, "\n  Run the plan with:\n")
		_, _ = fmt.Fprintf(out, "    snapd-repro-lp exec %d\n", plan.BugID)

		if planUsage != nil {
			_, _ = fmt.Fprintf(out, "\n%s\n",
				dim(fmt.Sprintf("Token usage: %d prompt + %d completion = %d total",
					planUsage.PromptTokens, planUsage.CompletionTokens, planUsage.TotalTokens)))
		}

		return nil
	},
}

// --- exec command ---

var execCmd = &cobra.Command{
	Use:   "exec [bug-ref]",
	Short: "Execute a reproduction plan in an LXD container",
	Long: `Look up a plan.json produced by the plan command for the given bug, launch an
LXD container with the specified Ubuntu version, and execute the reproduction
steps. The bug reference can be a numeric ID or a Launchpad URL.

The plan is expected at <output-dir>/bug-<ID>/plan.json.

Requires OPENROUTER_API_KEY to be set.`,
	Example: "  snapd-repro-lp exec 2137543",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bugID, err := parseBugRef(args[0])
		if err != nil {
			return fmt.Errorf("invalid bug reference: %w", err)
		}

		baseDir := outputDir
		if baseDir == "" {
			baseDir = "."
		}
		bugDir := filepath.Join(baseDir, fmt.Sprintf("bug-%s", bugID))
		planFile := filepath.Join(bugDir, "plan.json")

		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "%s Loading plan for bug #%s...\n", boldCyan("Step 1/3:"), bugID)

		plan, err := LoadPlan(planFile)
		if err != nil {
			return fmt.Errorf("loading plan: %w (run 'snapd-repro-lp plan %s' first)", err, bugID)
		}

		_, _ = fmt.Fprintf(out, "  %s\n", plan.Title)
		_, _ = fmt.Fprintf(out, "  Ubuntu version: %s\n", plan.UbuntuVersion)
		_, _ = fmt.Fprintf(out, "  Steps: %d\n", len(plan.Steps))

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		// Determine Ubuntu version: override flag takes precedence.
		ubuntuVersion := plan.UbuntuVersion
		if ubuntuOverride != "" {
			_, _ = fmt.Fprintf(out, "  Overriding Ubuntu version: %s → %s\n", plan.UbuntuVersion, ubuntuOverride)
			ubuntuVersion = ubuntuOverride
		}

		// Launch VM.
		instance, err := launchVM(cmd, ubuntuVersion, 2, 3)
		if err != nil {
			return err
		}
		defer cleanupVM(cmd, instance)

		resolveModel()
		_, _ = fmt.Fprintf(out, "\n%s Executing plan (model: %s)...\n", boldCyan("Step 3/3:"), modelName)

		result, usage, execLog, err := runExecutionAgent(ctx, cmd, plan, bugDir, instance)
		saveAgentLog(cmd, execLog, filepath.Join(bugDir, "execution-log.txt"))
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
		bug, bugDir, err := fetchAndPrepareBug(cmd, args[0], 1, 4)
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

		// Launch a default VM for investigation during planning.
		instance, err := launchVM(cmd, defaultUbuntuVersion, 2, 4)
		if err != nil {
			return err
		}
		// Ensure cleanup even if we relaunch below.
		defer func() {
			cleanupVM(cmd, instance)
		}()

		// Phase 1: Plan.
		resolveModel()
		_, _ = fmt.Fprintf(out, "\n%s Planning reproduction (model: %s)...\n", boldCyan("Step 3/4:"), modelName)

		plan, planUsage, planLog, err := runPlanningAgent(ctx, cmd, bug, bugDir, instance)
		saveAgentLog(cmd, planLog, filepath.Join(bugDir, "planning-log.txt"))
		if err != nil {
			return err
		}

		// Save plan.
		planFile := filepath.Join(bugDir, "plan.json")
		if err := SavePlan(plan, planFile); err != nil {
			return fmt.Errorf("saving plan: %w", err)
		}
		_, _ = fmt.Fprintf(out, "  Saved plan to %s\n", blue(fileHyperlink(planFile)))

		// Determine the execution Ubuntu version.
		execVersion := plan.UbuntuVersion
		if ubuntuOverride != "" {
			_, _ = fmt.Fprintf(out, "  Overriding Ubuntu version: %s → %s\n", plan.UbuntuVersion, ubuntuOverride)
			execVersion = ubuntuOverride
		}

		// If the plan requests a different version, relaunch the VM.
		if execVersion != defaultUbuntuVersion {
			_, _ = fmt.Fprintf(out, "  Plan requires ubuntu:%s, relaunching VM...\n", execVersion)
			cleanupVM(cmd, instance)
			newInstance := NewLXDManager()
			_, _ = fmt.Fprintf(out, "  Launching VM %s (ubuntu:%s)...\n", newInstance.Name(), execVersion)
			if err := newInstance.Launch(execVersion, "vm"); err != nil {
				return fmt.Errorf("relaunching VM: %w", err)
			}
			instance = newInstance
		}

		// Phase 2: Execute.
		_, _ = fmt.Fprintf(out, "\n%s Executing plan (model: %s)...\n", boldCyan("Step 4/4:"), modelName)

		result, execUsage, execLog, err := runExecutionAgent(ctx, cmd, plan, bugDir, instance)
		saveAgentLog(cmd, execLog, filepath.Join(bugDir, "execution-log.txt"))
		if err != nil {
			return err
		}

		saveExecutionResult(cmd, result, bugDir)

		// Combined token usage.
		var totalUsage Usage
		if planUsage != nil {
			totalUsage.PromptTokens += planUsage.PromptTokens
			totalUsage.CompletionTokens += planUsage.CompletionTokens
			totalUsage.TotalTokens += planUsage.TotalTokens
		}
		if execUsage != nil {
			totalUsage.PromptTokens += execUsage.PromptTokens
			totalUsage.CompletionTokens += execUsage.CompletionTokens
			totalUsage.TotalTokens += execUsage.TotalTokens
		}
		_, _ = fmt.Fprintf(out, "\n%s\n",
			dim(fmt.Sprintf("Token usage: %d prompt + %d completion = %d total",
				totalUsage.PromptTokens, totalUsage.CompletionTokens, totalUsage.TotalTokens)))

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
	execCmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "directory containing bug output (default: current directory)")
	execCmd.Flags().StringVar(&ubuntuOverride, "ubuntu", "", "override the Ubuntu version from the plan (e.g. 22.04)")

	// reproduce command flags (combines plan + exec).
	reproduceCmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "directory to write output (default: current directory)")
	reproduceCmd.Flags().BoolVarP(&forceOverwrite, "force", "f", false, "overwrite existing bug directory without prompting")
	reproduceCmd.Flags().StringVar(&ubuntuOverride, "ubuntu", "", "override the Ubuntu version from the plan (e.g. 22.04)")

	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(reproduceCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
