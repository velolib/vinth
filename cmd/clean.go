// cmd/clean.go
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/velolib/vinth/internal/lockfile"
)

var dryRun bool
var cleanYes bool

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove mod JAR files not in the lockfile",
	Long:  "Find and remove orphaned .jar files in the current directory that are not tracked by vinth.lock.json.",
	Example: `  vinth clean
  vinth clean --dry-run
  vinth clean --yes`,
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()
		t := termenv.ColorProfile()
		red := termenv.String().Foreground(t.Color("9")).Bold()
		green := termenv.String().Foreground(t.Color("10")).Bold()
		white := termenv.String().Foreground(t.Color("15")).Bold()

		lf, err := lockfile.Load()
		if err != nil {
			out.Error(fmt.Sprintf("Failed to read lockfile: %v", err))
			os.Exit(1)
		}

		// Get all .jar files in current directory
		entries, err := os.ReadDir(".")
		if err != nil {
			out.Error(fmt.Sprintf("Failed to read current directory: %v", err))
			os.Exit(1)
		}

		// Build a set of files that should exist
		trackedFiles := make(map[string]bool)
		for _, entry := range lf.Mods {
			trackedFiles[entry.FileName] = true
		}

		// Find orphaned JAR files
		var orphanedFiles []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jar") {
				if !trackedFiles[entry.Name()] {
					orphanedFiles = append(orphanedFiles, entry.Name())
				}
			}
		}

		if len(orphanedFiles) == 0 {
			out.Success("No orphaned JAR files found.")
			return
		}

		out.Info(fmt.Sprintf("Found %d orphaned JAR file(s):", len(orphanedFiles)))
		for _, file := range orphanedFiles {
			fmt.Println(white.Styled(fmt.Sprintf("  • %s", file)))
		}
		out.Blank()

		if dryRun {
			out.Tip("(dry-run) Would delete the above files. Run without --dry-run to actually delete.")
			return
		}

		confirmDelete := cleanYes
		if !cleanYes {
			if err := huh.NewConfirm().
				Title(fmt.Sprintf("Delete %d orphaned JAR file(s)?", len(orphanedFiles))).
				Affirmative("Delete").
				Negative("Cancel").
				Value(&confirmDelete).
				Run(); err != nil {
				out.Warn("Delete cancelled.")
				confirmDelete = false
			}
		}

		if !confirmDelete {
			out.Tip("No files were deleted.")
			out.Blank()
			out.Summary("Clean", metric("candidates", len(orphanedFiles)), metric("deleted", 0), metric("failed", 0), metric("cancelled", 1))
			return
		}

		var deletedCount int
		var failedCount int
		pbar, bar := newStandardProgress(len(orphanedFiles), "Deleting files ", green)
		for _, file := range orphanedFiles {
			if err := os.Remove(file); err != nil {
				failedCount++
				fmt.Println(red.Styled(fmt.Sprintf("❌ Failed to delete %s: %v", file, err)))
			} else {
				fmt.Println(green.Styled(fmt.Sprintf("🗑️  Deleted: %s", file)))
				deletedCount++
			}
			bar.Increment()
		}
		pbar.Wait()

		out.Blank()
		out.Success(fmt.Sprintf("Deleted %d file(s).", deletedCount))
		out.Summary("Clean", metric("candidates", len(orphanedFiles)), metric("deleted", deletedCount), metric("failed", failedCount), metric("cancelled", 0))
	},
}

func init() {
	cleanCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what would be deleted without actually deleting")
	cleanCmd.Flags().BoolVar(&cleanYes, "yes", false, "Skip delete confirmation prompt")
	rootCmd.AddCommand(cleanCmd)
}
