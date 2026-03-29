package cmd

import (
	stderrors "errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"github.com/velolib/vinth/internal/api"
	vinthErrors "github.com/velolib/vinth/internal/errors"
	"github.com/velolib/vinth/internal/lockfile"
	"github.com/velolib/vinth/internal/utils"
	"golang.org/x/term"
)

type compatibilityPreview struct {
	Compatible   []string
	Incompatible []string
	FetchErrors  map[string]error
}

var editCmd = &cobra.Command{
	Use:     "edit",
	Short:   "Interactively edit lockfile game version and loader",
	Long:    "Interactively change Minecraft version/loader, preview compatibility, remove incompatible mods, and refresh remaining mods.",
	Example: `  vinth edit`,
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()

		if !lockfile.Exists() {
			out.Error("No vinth.lock.json found. Run 'vinth create' first.")
			os.Exit(1)
		}

		lf, err := lockfile.Load()
		if err != nil {
			out.Error(fmt.Sprintf("Failed to read lockfile: %v", err))
			os.Exit(1)
		}

		var versionFilter string
		newGameVersion := lf.GameVersion
		newLoader := lf.Loader

		if err := huh.NewSelect[string]().
			Title("Which Minecraft versions do you want to see?").
			Options(
				huh.NewOption("Releases Only (Recommended)", "release"),
				huh.NewOption("All Versions (Snapshots, Betas, etc.)", "all"),
			).
			Value(&versionFilter).
			Run(); err != nil {
			out.Warn("Edit cancelled.")
			return
		}

		out.Info("Fetching Minecraft versions from Mojang...")
		versions, err := api.FetchMinecraftVersions(versionFilter == "release")
		if err != nil {
			out.Error(fmt.Sprintf("Failed to fetch versions: %v", err))
			os.Exit(1)
		}

		newGameVersion, err = selectMinecraftVersion(newGameVersion, versions)
		if err != nil {
			out.Warn("Edit cancelled.")
			return
		}

		loaderOptions := []string{"fabric", "forge", "quilt", "neoforge"}
		loaderHeight := pickerHeight(len(loaderOptions))
		if err := huh.NewSelect[string]().
			Title("Select Mod Loader").
			Options(
				huh.NewOption("Fabric", "fabric"),
				huh.NewOption("NeoForge", "neoforge"),
				huh.NewOption("Quilt", "quilt"),
				huh.NewOption("Forge", "forge"),
			).
			Value(&newLoader).
			Height(loaderHeight).
			Run(); err != nil {
			out.Warn("Edit cancelled.")
			return
		}

		out.Blank()
		out.Info(fmt.Sprintf("Previewing compatibility for Minecraft %s (%s)...", newGameVersion, newLoader))
		preview := previewModCompatibility(lf, newGameVersion, newLoader)
		if len(preview.FetchErrors) > 0 {
			out.Blank()
			out.Error(fmt.Sprintf("Failed to check compatibility for %d mod(s).", len(preview.FetchErrors)))
			out.Warn("API errors must be resolved before applying changes.")
			os.Exit(1)
		}

		out.Blank()
		out.Info(fmt.Sprintf("Current target: %s (%s)", lf.GameVersion, lf.Loader))
		out.Info(fmt.Sprintf("New target: %s (%s)", newGameVersion, newLoader))
		out.Success(fmt.Sprintf("Compatible mods: %d", len(preview.Compatible)))
		if len(preview.Incompatible) > 0 {
			out.Warn(fmt.Sprintf("Incompatible mods: %d", len(preview.Incompatible)))
			out.Info("These mods are not compatible with the selected target:")
			for _, slug := range preview.Incompatible {
				fmt.Printf("  - %s\n", slug)
			}
		} else {
			out.Success("No incompatible mods detected in preview.")
		}

		confirmApply := false
		if err := huh.NewConfirm().
			Title("Apply these changes to vinth.lock.json? Incompatible mods will be removed.").
			Affirmative("Apply").
			Negative("Cancel").
			Value(&confirmApply).
			Run(); err != nil {
			out.Warn("Edit cancelled.")
			return
		}

		if !confirmApply {
			out.Warn("No changes were applied.")
			return
		}

		lf.GameVersion = newGameVersion
		lf.Loader = newLoader

		removed := make([]string, 0, len(preview.Incompatible))
		for _, slug := range preview.Incompatible {
			if _, exists := lf.Mods[slug]; exists {
				delete(lf.Mods, slug)
				removed = append(removed, slug)
			}
		}

		autoUpgradedCount := 0
		if len(lf.Mods) > 0 {
			out.Blank()
			out.Info("Upgrading remaining mods for the selected target...")

			t := termenv.ColorProfile()
			bold := termenv.String().Bold()
			green := termenv.String().Foreground(t.Color("10")).Bold()
			yellow := termenv.String().Foreground(t.Color("11")).Bold()
			cyan := termenv.String().Foreground(t.Color("14")).Bold()
			white := termenv.String().Foreground(t.Color("15")).Bold()

			var wg sync.WaitGroup
			var mu sync.Mutex
			mpbStyle := mpb.WithWidth(40)
			pbar := mpb.New(mpbStyle)
			bar := pbar.New(int64(len(lf.Mods)),
				mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("·").Rbound("╟"),
				mpb.PrependDecorators(
					decor.Name(green.Styled("Checking API "), decor.WC{W: 16, C: decor.DindentRight}),
					decor.CountersNoUnit(bold.Styled("%d / %d")),
				),
				mpb.AppendDecorators(
					decor.Percentage(decor.WCSyncWidth),
				),
			)

			for slug, entry := range lf.Mods {
				wg.Add(1)
				go func(modSlug string, currentEntry lockfile.ModEntry) {
					defer wg.Done()
					latestInfo, fetchErr := api.FetchLatestVersion(modSlug, lf.GameVersion, lf.Loader)

					mu.Lock()
					defer mu.Unlock()

					var statusMsg string
					if fetchErr != nil || len(latestInfo.Files) == 0 {
						statusMsg = fmt.Sprintf("⚠️  %s: %s", white.Styled(modSlug), yellow.Styled("Failed to fetch data"))
					} else if latestInfo.ID != currentEntry.VersionID {
						primaryFile := latestInfo.Files[0]
						safeFileName, sanitizeErr := utils.SanitizeModFileName(primaryFile.Filename)
						if sanitizeErr != nil {
							statusMsg = fmt.Sprintf("⚠️  %s: %s", white.Styled(modSlug), yellow.Styled(fmt.Sprintf("Invalid file name from API (%v)", sanitizeErr)))
						} else {
							lf.Mods[modSlug] = lockfile.ModEntry{
								ProjectID:   latestInfo.ProjectID,
								VersionID:   latestInfo.ID,
								FileName:    safeFileName,
								DownloadURL: primaryFile.URL,
								FileSize:    primaryFile.Size,
								Hash:        primaryFile.Hashes.Sha512,
							}
							autoUpgradedCount++
							statusMsg = fmt.Sprintf("⬆️  %s: %s", white.Styled(modSlug), cyan.Styled(fmt.Sprintf("Upgraded (%s -> %s)", currentEntry.VersionID, latestInfo.ID)))
						}
					} else {
						statusMsg = fmt.Sprintf("✅ %s: %s", white.Styled(modSlug), green.Styled("Already up to date"))
					}

					pbar.Write([]byte(statusMsg + "\n"))
					bar.Increment()
				}(slug, entry)
			}

			wg.Wait()
			pbar.Wait()
		}

		if err := lf.Save(); err != nil {
			out.Error(fmt.Sprintf("Failed to save lockfile: %v", err))
			os.Exit(1)
		}

		out.Blank()
		out.Success(fmt.Sprintf("Updated lockfile target to Minecraft %s (%s).", newGameVersion, newLoader))
		if len(removed) > 0 {
			out.Warn(fmt.Sprintf("Removed %d incompatible mod(s) from the lockfile.", len(removed)))
			for _, slug := range removed {
				fmt.Printf("  - %s\n", slug)
			}
		}
		if autoUpgradedCount > 0 {
			out.Success(fmt.Sprintf("Auto-upgraded %d remaining mod(s) for the selected target.", autoUpgradedCount))
		}

		out.Blank()
		out.Info("Checking dependencies for remaining mods...")
		depResult := checkDependenciesWithProgress(lf, nil, nil, termenv.ColorProfile())
		printDependencySummary(depResult, termenv.ColorProfile(), false)
		if len(depResult.FetchErrors) > 0 {
			out.Warn("Some mods could not be checked due to API errors.")
		}
		if len(depResult.MissingByMod) > 0 {
			missing := uniqueMissingDependencies(depResult.MissingByMod)
			out.Warn(fmt.Sprintf("Remaining mods are missing required dependencies: %s", strings.Join(missing, ", ")))
			out.Tip("Run: vinth deps --add")
		} else if len(depResult.FetchErrors) == 0 {
			out.Success("All required dependencies are present after edit.")
		} else {
			out.Warn("Dependency check completed with API errors.")
		}

	},
}

func init() {
	rootCmd.AddCommand(editCmd)
}

func pickerHeight(optionCount int) int {
	const (
		minHeight      = 3
		terminalBuffer = 4
		maxVisibleRows = 18
	)

	termHeight := 10
	if fd := int(os.Stdout.Fd()); term.IsTerminal(fd) {
		if _, h, err := term.GetSize(fd); err == nil {
			termHeight = h
		}
	}

	height := termHeight - terminalBuffer
	if height < minHeight {
		height = minHeight
	}

	// Rendering fewer rows keeps huge lists (e.g. all MC versions) responsive.
	if height > maxVisibleRows {
		height = maxVisibleRows
	}

	if optionCount > 0 && optionCount < height {
		height = optionCount
	}

	return height
}

func previewModCompatibility(lf *lockfile.Lockfile, gameVersion string, loader string) compatibilityPreview {
	preview := compatibilityPreview{
		Compatible:   []string{},
		Incompatible: []string{},
		FetchErrors:  make(map[string]error),
	}

	if len(lf.Mods) == 0 {
		return preview
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 8)

	t := termenv.ColorProfile()
	bold := termenv.String().Bold()
	green := termenv.String().Foreground(t.Color("10")).Bold()
	yellow := termenv.String().Foreground(t.Color("11")).Bold()
	red := termenv.String().Foreground(t.Color("9")).Bold()
	white := termenv.String().Foreground(t.Color("15")).Bold()

	mpbStyle := mpb.WithWidth(40)
	pbar := mpb.New(mpbStyle)
	bar := pbar.New(int64(len(lf.Mods)),
		mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("·").Rbound("╟"),
		mpb.PrependDecorators(
			decor.Name(green.Styled("Checking API "), decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit(bold.Styled("%d / %d")),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WCSyncWidth),
		),
	)

	for slug := range lf.Mods {
		wg.Add(1)
		go func(modSlug string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			versionInfo, err := api.FetchLatestVersion(modSlug, gameVersion, loader)

			mu.Lock()
			defer mu.Unlock()
			var statusMsg string
			if err != nil {
				// Treat "notfound" as incompatibility for the selected target, not as an API failure.
				var appErr *vinthErrors.AppError
				if stderrors.As(err, &appErr) && appErr.Code == "notfound" {
					statusMsg = fmt.Sprintf("⚠️  %s: %s", white.Styled(modSlug), yellow.Styled("Incompatible with selected target"))
					preview.Incompatible = append(preview.Incompatible, modSlug)
					pbar.Write([]byte(statusMsg + "\n"))
					bar.Increment()
					return
				}

				preview.FetchErrors[modSlug] = err
				statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(modSlug), red.Styled("Failed to fetch compatibility data"))
				preview.Incompatible = append(preview.Incompatible, modSlug)
				pbar.Write([]byte(statusMsg + "\n"))
				bar.Increment()
				return
			}
			if len(versionInfo.Files) == 0 {
				statusMsg = fmt.Sprintf("⚠️  %s: %s", white.Styled(modSlug), yellow.Styled("Incompatible with selected target"))
				preview.Incompatible = append(preview.Incompatible, modSlug)
				pbar.Write([]byte(statusMsg + "\n"))
				bar.Increment()
				return
			}
			statusMsg = fmt.Sprintf("✅ %s: %s", white.Styled(modSlug), green.Styled("Compatible"))
			preview.Compatible = append(preview.Compatible, modSlug)
			pbar.Write([]byte(statusMsg + "\n"))
			bar.Increment()
		}(slug)
	}

	wg.Wait()
	pbar.Wait()
	sort.Strings(preview.Compatible)
	sort.Strings(preview.Incompatible)
	return preview
}
