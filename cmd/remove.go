// cmd/remove.go
package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/velolib/vinth/internal/api"
	"github.com/velolib/vinth/internal/lockfile"
)

var removeByID bool

var removeCmd = &cobra.Command{
	Use:   "remove [mod-identifiers...]",
	Short: "Remove one or more mods from the lockfile",
	Long:  "Remove mods by slug or by Modrinth project ID, or run with no arguments for interactive selection.",
	Example: `  vinth remove sodium fabric-api
  vinth remove --id AANobbMI P7dR8mSH
  vinth remove`,
	Args: cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()

		if !lockfile.Exists() {
			out.Error("No vinth.lock.json found. Nothing to remove.")
			os.Exit(1)
		}

		lf, err := lockfile.Load()
		if err != nil {
			out.Error(fmt.Sprintf("Failed to load lockfile: %v", err))
			os.Exit(1)
		}

		if len(lf.Mods) == 0 {
			out.Warn("Lockfile is empty. Nothing to remove.")
			return
		}

		targets := make([]string, 0, len(args))
		targets = append(targets, args...)

		if len(targets) == 0 {
			selected, selectErr := selectModsForRemoval(lf)
			if selectErr != nil {
				out.Warn("Remove cancelled.")
				return
			}

			if len(selected) == 0 {
				out.Warn("No mods selected.")
				return
			}

			confirmRemove := false
			if err := huh.NewConfirm().
				Title(fmt.Sprintf("Remove %d selected mod(s) from vinth.lock.json?", len(selected))).
				Affirmative("Remove").
				Negative("Cancel").
				Value(&confirmRemove).
				Run(); err != nil {
				out.Warn("Remove cancelled.")
				return
			}

			if !confirmRemove {
				out.Warn("Remove cancelled.")
				return
			}

			targets = selected
		}

		resolved := make([]string, 0, len(targets))
		seen := make(map[string]struct{}, len(targets))
		resolveFailed := 0

		if removeByID {
			out.Info(fmt.Sprintf("Removing %d mod(s) by Modrinth project ID...", len(targets)))
		} else {
			out.Info(fmt.Sprintf("Removing %d mod(s) by slug...", len(targets)))
		}

		for _, target := range targets {
			slug := target
			if removeByID {
				resolvedSlug, resolveErr := api.FetchProjectSlug(target)
				if resolveErr != nil {
					resolveFailed++
					out.Warn(fmt.Sprintf("%s: Failed to resolve project ID (%v)", target, resolveErr))
					continue
				}
				slug = resolvedSlug
			}

			if _, exists := seen[slug]; exists {
				continue
			}
			seen[slug] = struct{}{}
			resolved = append(resolved, slug)
		}

		if len(resolved) == 0 {
			out.Error("No valid mods were resolved for removal.")
			os.Exit(1)
		}

		removedCount := 0
		notFoundCount := 0
		for _, modSlug := range resolved {
			if _, exists := lf.Mods[modSlug]; !exists {
				notFoundCount++
				out.Warn(fmt.Sprintf("%s: Mod is not in your lockfile.", modSlug))
				continue
			}

			delete(lf.Mods, modSlug)
			removedCount++
			out.Success(fmt.Sprintf("%s: Removed from vinth.lock.json", modSlug))
		}

		if removedCount > 0 {
			if err := lf.Save(); err != nil {
				out.Error(fmt.Sprintf("Failed to save lockfile: %v", err))
				os.Exit(1)
			}
		}

		out.Blank()
		out.Summary("Remove", metric("requested", len(targets)), metric("resolved", len(resolved)), metric("removed", removedCount), metric("not_found", notFoundCount), metric("resolve_failed", resolveFailed))

		if removedCount == 0 {
			out.Warn("No mods were removed from vinth.lock.json.")
			os.Exit(1)
		}

		if notFoundCount > 0 || resolveFailed > 0 {
			out.Warn("Removal completed with warnings.")
			os.Exit(1)
		}

		out.Success(fmt.Sprintf("Successfully removed %d mod(s).", removedCount))
	},
}

func selectModsForRemoval(lf *lockfile.Lockfile) ([]string, error) {
	slugs := make([]string, 0, len(lf.Mods))
	for slug := range lf.Mods {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	options := make([]huh.Option[string], 0, len(slugs))
	for _, slug := range slugs {
		entry := lf.Mods[slug]
		label := fmt.Sprintf("%s (%s)", slug, entry.VersionID)
		options = append(options, huh.NewOption(label, slug))
	}

	selected := []string{}
	err := huh.NewMultiSelect[string]().
		Title("Select Mod(s) To Remove").
		Description("Use space to toggle selections, then enter to continue.").
		Options(options...).
		Value(&selected).
		Height(pickerHeight(len(options))).
		Run()

	return selected, err
}

func init() {
	removeCmd.Flags().BoolVar(&removeByID, "id", false, "Treat all arguments as Modrinth project IDs")
	removeCmd.Flags().BoolVar(&removeByID, "modrinth-id", false, "Treat all arguments as Modrinth project IDs")
	rootCmd.AddCommand(removeCmd)
}
