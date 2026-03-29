// cmd/create.go
package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/velolib/vinth/internal/api"
	"github.com/velolib/vinth/internal/lockfile"
)

var createCmd = &cobra.Command{
	Use:     "create",
	Short:   "Initialize a new vinth.lock.json wizard",
	Long:    "Start an interactive wizard to create or overwrite vinth.lock.json with a Minecraft version and mod loader.",
	Example: `  vinth create`,
	Run: func(cmd *cobra.Command, args []string) {
		RunCreateWizard()
	},
}

func init() {
	rootCmd.AddCommand(createCmd)
}

// RunCreateWizard is exported so other commands (like 'add') can trigger it
func RunCreateWizard() {
	out := newCmdOutput()
	var versionFilter string
	var mcVersion string
	var loader string

	if lockfile.Exists() {
		out.Warn("vinth.lock.json already exists. Creating a new one will overwrite it.")
		confirmOverwrite := false
		if err := huh.NewConfirm().
			Title("Overwrite existing vinth.lock.json?").
			Affirmative("Overwrite").
			Negative("Cancel").
			Value(&confirmOverwrite).
			Run(); err != nil {
			out.Warn("Create cancelled.")
			return
		}

		if !confirmOverwrite {
			out.Warn("Create cancelled. Existing vinth.lock.json was kept.")
			return
		}
	}

	// Step 1: Ask what kind of versions they want to see
	err := huh.NewSelect[string]().
		Title("Which Minecraft versions do you want to see?").
		Options(
			huh.NewOption("Releases Only (Recommended)", "release"),
			huh.NewOption("All Versions (Snapshots, Betas, etc.)", "all"),
		).
		Value(&versionFilter).
		Run()

	if err != nil {
		out.Warn("Create cancelled.")
		return
	}

	out.Info("Fetching Minecraft versions from Mojang...")
	versions, err := api.FetchMinecraftVersions(versionFilter == "release")
	if err != nil {
		out.Error(fmt.Sprintf("Failed to fetch versions: %v", err))
		os.Exit(1)
	}

	// Step 2: Select Minecraft Version.
	mcVersion, err = selectMinecraftVersion(mcVersion, versions)
	if err != nil {
		out.Warn("Create cancelled.")
		return
	}

	// Step 3: Select Mod Loader (after version)
	modLoaderOptions := []string{"fabric", "forge", "quilt", "neoforge"}
	modLoaderHeight := pickerHeight(len(modLoaderOptions))
	err = huh.NewSelect[string]().
		Title("Select Mod Loader").
		Options(
			huh.NewOption("Fabric", "fabric"),
			huh.NewOption("NeoForge", "neoforge"),
			huh.NewOption("Quilt", "quilt"),
			huh.NewOption("Forge", "forge"),
		).
		Value(&loader).
		Height(modLoaderHeight).
		Run()
	if err != nil {
		out.Warn("Create cancelled.")
		return
	}

	// Save to lockfile
	lf := &lockfile.Lockfile{
		GameVersion: mcVersion,
		Loader:      loader,
		Mods:        make(map[string]lockfile.ModEntry),
	}

	if err := lf.Save(); err != nil {
		out.Error(fmt.Sprintf("Failed to save lockfile: %v", err))
		os.Exit(1)
	}

	out.Success(fmt.Sprintf("Created vinth.lock.json (Minecraft %s, %s)", mcVersion, loader))
}
