package cmd

import (
	"fmt"
	"os"

	"github.com/velolib/vinth/internal/errors"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "vinth",
	Short: "A lockfile-based Modrinth mod manager",
	Long: `vinth is a Minecraft mod manager that tracks Modrinth mods in vinth.lock.json.

vinth is designed for reproducible mod folders across machines.`,
	Example: `  vinth create
  vinth add sodium fabric-api
  vinth deps --add
  vinth sync
  vinth list`,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		// Print a user-friendly error message
		fmt.Fprintln(os.Stderr, errors.UserMessage(err))
		// Optionally, print the full error for debugging
		// fmt.Fprintf(os.Stderr, "DEBUG: %+v\n", err)
		os.Exit(1)
	}
}

func init() {

}
