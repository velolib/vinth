// cmd/list.go
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/velolib/vinth/internal/lockfile"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "Display all mods in the lockfile",
	Long:    "Print every mod currently tracked in vinth.lock.json.",
	Example: `  vinth list`,
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()
		t := termenv.ColorProfile()
		bold := termenv.String().Bold()
		white := termenv.String().Foreground(t.Color("15")).Bold()

		if !lockfile.Exists() {
			out.Error("No vinth.lock.json found.")
			os.Exit(1)
		}

		lf, err := lockfile.Load()
		if err != nil {
			out.Error(fmt.Sprintf("Failed to read lockfile: %v", err))
			os.Exit(1)
		}

		if len(lf.Mods) == 0 {
			out.Warn("Lockfile is empty.")
			return
		}

		out.Info(fmt.Sprintf("Minecraft %s (%s)", lf.GameVersion, lf.Loader))
		out.Blank()

		// Print header
		headerLine := fmt.Sprintf("%-30s %-20s %-8s %s", "MOD SLUG", "VERSION", "LOCKED", "LINK")
		fmt.Println(white.Styled(bold.Styled(headerLine)))
		fmt.Println(white.Styled(bold.Styled(strings.Repeat("━", len(headerLine)))))

		// Print each mod
		for slug, entry := range lf.Mods {
			modrinthURL := fmt.Sprintf("https://modrinth.com/mod/%s", slug)
			modLink := termenv.Hyperlink(modrinthURL, "View")
			versionName := entry.VersionName
			if versionName == "" {
				versionName = entry.VersionID
			}
			lockState := "no"
			if entry.VersionLock {
				lockState = "yes"
			}
			fmt.Printf("%-30s %-20s %-8s %s\n", slug, versionName, lockState, modLink)
		}

		out.Blank()
		out.Success(fmt.Sprintf("Total: %d mod(s)", len(lf.Mods)))
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
