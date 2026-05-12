package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var verbose bool

var rootCmd = &cobra.Command{
	Use:   "snapd-repro-lp",
	Short: "Reproduce snapd bugs from Launchpad",
	Long:  "snapd-repro-lp creates reproducers for bugs reported against snapd on Launchpad.",
}

var reproduceCmd = &cobra.Command{
	Use:   "reproduce",
	Short: "Create a reproducer for a Launchpad bug",
	Long:  "Analyse a Launchpad bug report and produce a minimal reproducer.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bug := args[0]
		if verbose {
			fmt.Fprintf(cmd.ErrOrStderr(), "verbose: reproducing bug %s\n", bug)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Reproducing Launchpad bug: %s\n", bug)
		// TODO: implement reproduction logic
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output")
	rootCmd.AddCommand(reproduceCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
