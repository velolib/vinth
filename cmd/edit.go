package cmd

import (
	stderrors "errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
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
	Short:   "Interactively edit lockfile target settings and version locks",
	Long:    "Interactively change Minecraft version/loader, preview compatibility, remove incompatible mods, refresh remaining mods, toggle version locks, or select specific mod versions.",
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

		var editAction string
		if err := huh.NewSelect[string]().
			Title("What do you want to edit?").
			Options(
				huh.NewOption("Minecraft version", "version"),
				huh.NewOption("Mod loader", "loader"),
				huh.NewOption("Version management", "version_management"),
				huh.NewOption("Exit", "exit"),
			).
			Value(&editAction).
			Run(); err != nil {
			out.Warn("Edit cancelled.")
			return
		}

		if editAction == "exit" {
			out.Info("Exited edit menu.")
			return
		}

		if editAction == "version_management" {
			runVersionManagement(lf, out)
			return
		}

		newGameVersion := lf.GameVersion
		newLoader := lf.Loader

		if editAction == "version" {
			var versionFilter string
			if err := huh.NewSelect[string]().
				Title("Which Minecraft versions do you want to see?").
				Options(
					huh.NewOption("Releases Only (Recommended)", "release"),
					huh.NewOption("All Versions (Snapshots, Betas, etc.)", "all"),
					huh.NewOption("Exit", "exit"),
				).
				Value(&versionFilter).
				Run(); err != nil {
				out.Warn("Edit cancelled.")
				return
			}

			if versionFilter == "exit" {
				out.Info("Exited edit menu.")
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
		}

		if editAction == "loader" {
			loaderOptions := []string{"fabric", "forge", "quilt", "neoforge"}
			loaderHeight := pickerHeight(len(loaderOptions))
			if err := huh.NewSelect[string]().
				Title("Select Mod Loader").
				Options(
					huh.NewOption("Fabric", "fabric"),
					huh.NewOption("NeoForge", "neoforge"),
					huh.NewOption("Quilt", "quilt"),
					huh.NewOption("Forge", "forge"),
					huh.NewOption("Exit", "exit"),
				).
				Value(&newLoader).
				Height(loaderHeight).
				Run(); err != nil {
				out.Warn("Edit cancelled.")
				return
			}

			if newLoader == "exit" {
				out.Info("Exited edit menu.")
				return
			}
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

		if !confirmEditApply(out) {
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
		skippedLockedCount := 0
		if len(lf.Mods) > 0 {
			out.Blank()
			out.Info("Upgrading remaining mods for the selected target...")

			t := termenv.ColorProfile()
			green := termenv.String().Foreground(t.Color("10")).Bold()
			yellow := termenv.String().Foreground(t.Color("11")).Bold()
			cyan := termenv.String().Foreground(t.Color("14")).Bold()
			white := termenv.String().Foreground(t.Color("15")).Bold()

			var wg sync.WaitGroup
			var mu sync.Mutex
			pbar, bar := newStandardProgress(len(lf.Mods), "Checking API ", green)

			for slug, entry := range lf.Mods {
				wg.Add(1)
				go func(modSlug string, currentEntry lockfile.ModEntry) {
					defer wg.Done()
					if currentEntry.VersionLock {
						lockedVersion := currentEntry.VersionName
						if lockedVersion == "" {
							lockedVersion = currentEntry.VersionID
						}
						mu.Lock()
						skippedLockedCount++
						statusMsg := fmt.Sprintf("🔒 %s: %s", white.Styled(modSlug), yellow.Styled(fmt.Sprintf("Skipped (locked at %s)", lockedVersion)))
						pbar.Write([]byte(statusMsg + "\n"))
						bar.Increment()
						mu.Unlock()
						return
					}
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
							versionName := latestInfo.VersionName
							if versionName == "" {
								versionName = latestInfo.ID
							}
							lf.Mods[modSlug] = lockfile.ModEntry{
								ProjectID:   latestInfo.ProjectID,
								VersionID:   latestInfo.ID,
								VersionName: versionName,
								VersionLock: currentEntry.VersionLock,
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
		if skippedLockedCount > 0 {
			out.Info(fmt.Sprintf("Skipped %d version-locked mod(s) during auto-upgrade.", skippedLockedCount))
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

func runVersionManagement(lf *lockfile.Lockfile, out cmdOutput) {
	if len(lf.Mods) == 0 {
		out.Warn("Lockfile is empty.")
		return
	}

	stagedMods := cloneMods(lf.Mods)
	t := termenv.ColorProfile()
	changedStyle := termenv.String().Foreground(t.Color("11")).Bold()

	for {
		slugs := make([]string, 0, len(stagedMods))
		for slug := range stagedMods {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)

		options := make([]huh.Option[string], 0, len(slugs)+2)
		options = append(options,
			huh.NewOption("Apply version management changes", "__apply"),
			huh.NewOption("Exit without applying", "__exit"),
		)
		for _, slug := range slugs {
			entry := stagedMods[slug]
			versionName := compactStagedVersionLabel(entry)
			lockState := "U"
			if entry.VersionLock {
				lockState = "L"
			}
			label := fmt.Sprintf("%s (%s) [%s]", slug, versionName, lockState)
			if hasStagedModChange(lf.Mods[slug], entry) {
				label += changedStyle.Styled(" [changed]")
			}
			options = append(options, huh.NewOption(label, slug))
		}

		selection := ""
		err := huh.NewSelect[string]().
			Title("Version management").
			Description("Open a mod to edit version/lock, then apply once when done.").
			Options(options...).
			Value(&selection).
			Height(pickerHeight(len(options))).
			Run()
		if err != nil {
			out.Warn("Edit cancelled.")
			return
		}

		switch selection {
		case "__exit":
			out.Warn("No changes were applied.")
			return
		case "__apply":
			versionChanges, lockChanges := summarizeVersionManagementChanges(lf.Mods, stagedMods)
			if versionChanges == 0 && lockChanges == 0 {
				out.Warn("No changes were staged.")
				return
			}

			out.Blank()
			out.Summary("Version management", metric("version_changes", versionChanges), metric("lock_changes", lockChanges))
			if !confirmEditApply(out) {
				return
			}

			lf.Mods = stagedMods
			if err := lf.Save(); err != nil {
				out.Error(fmt.Sprintf("Failed to save lockfile: %v", err))
				os.Exit(1)
			}

			out.Blank()
			out.Success("Applied version management changes to vinth.lock.json.")
			return
		default:
			runSingleModVersionManagement(selection, stagedMods, lf.GameVersion, lf.Loader, out)
		}
	}
}

func runSingleModVersionManagement(slug string, stagedMods map[string]lockfile.ModEntry, gameVersion string, loader string, out cmdOutput) {
	for {
		entry := stagedMods[slug]
		versionName := entry.VersionName
		if versionName == "" {
			versionName = entry.VersionID
		}
		lockState := "unlocked"
		if entry.VersionLock {
			lockState = "locked"
		}

		action := ""
		err := huh.NewSelect[string]().
			Title(fmt.Sprintf("Manage %s", slug)).
			Description(fmt.Sprintf("Current version: %s | Lock: %s", versionName, lockState)).
			Options(
				huh.NewOption("Edit version (fetch versions first)", "edit_version"),
				huh.NewOption("Toggle version lock", "toggle_lock"),
				huh.NewOption("Go back", "back"),
			).
			Value(&action).
			Run()
		if err != nil {
			out.Warn("Edit cancelled.")
			return
		}

		switch action {
		case "back":
			return
		case "toggle_lock":
			entry.VersionLock = !entry.VersionLock
			if entry.VersionName == "" {
				entry.VersionName = entry.VersionID
			}
			stagedMods[slug] = entry
			state := "unlocked"
			if entry.VersionLock {
				state = "locked"
			}
			out.Info(fmt.Sprintf("Staged: %s lock is now %s.", slug, state))
		case "edit_version":
			out.Info(fmt.Sprintf("Fetching versions for %s...", slug))
			selectedVersion, cancelled, selectErr := selectVersionForEdit(slug, entry.VersionID, gameVersion, loader)
			if cancelled {
				out.Warn("Version selection cancelled.")
				continue
			}
			if selectErr != nil {
				out.Warn(fmt.Sprintf("Failed to select version: %v", selectErr))
				continue
			}
			if len(selectedVersion.Files) == 0 {
				out.Warn("Selected version has no downloadable files.")
				continue
			}

			primaryFile := selectedVersion.Files[0]
			safeFileName, sanitizeErr := utils.SanitizeModFileName(primaryFile.Filename)
			if sanitizeErr != nil {
				out.Warn(fmt.Sprintf("Invalid file name from API (%v)", sanitizeErr))
				continue
			}

			newVersionName := selectedVersion.VersionName
			if newVersionName == "" {
				newVersionName = selectedVersion.ID
			}

			entry.ProjectID = selectedVersion.ProjectID
			entry.VersionID = selectedVersion.ID
			entry.VersionName = newVersionName
			entry.FileName = safeFileName
			entry.DownloadURL = primaryFile.URL
			entry.FileSize = primaryFile.Size
			entry.Hash = primaryFile.Hashes.Sha512
			stagedMods[slug] = entry

			out.Info(fmt.Sprintf("Staged: %s version set to %s.", slug, newVersionName))
		}
	}
}

func summarizeVersionManagementChanges(current map[string]lockfile.ModEntry, staged map[string]lockfile.ModEntry) (int, int) {
	versionChanges := 0
	lockChanges := 0
	for slug, original := range current {
		updated, exists := staged[slug]
		if !exists {
			continue
		}
		if original.VersionID != updated.VersionID {
			versionChanges++
		}
		if original.VersionLock != updated.VersionLock {
			lockChanges++
		}
	}
	return versionChanges, lockChanges
}

func cloneMods(mods map[string]lockfile.ModEntry) map[string]lockfile.ModEntry {
	cloned := make(map[string]lockfile.ModEntry, len(mods))
	for slug, entry := range mods {
		cloned[slug] = entry
	}
	return cloned
}

func hasStagedModChange(original lockfile.ModEntry, staged lockfile.ModEntry) bool {
	return original.VersionID != staged.VersionID || original.VersionLock != staged.VersionLock
}

func compactStagedVersionLabel(entry lockfile.ModEntry) string {
	versionName := entry.VersionName
	if versionName == "" {
		versionName = entry.VersionID
	}
	return truncateEditLabel(sanitizeEditTerminalLabel(versionName), 28)
}

func truncateEditLabel(value string, maxRunes int) string {
	if maxRunes <= 3 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-3]) + "..."
}

func selectVersionForEdit(slug string, currentVersionID string, gameVersion string, loader string) (*api.ModrinthVersion, bool, error) {
	versions, err := api.FetchProjectVersions(slug, gameVersion, loader)
	if err != nil {
		return nil, false, err
	}
	if len(versions) == 0 {
		return nil, false, fmt.Errorf("no compatible versions found")
	}

	options := make([]huh.Option[string], 0, len(versions)+1)
	versionMap := make(map[string]api.ModrinthVersion, len(versions))
	for _, version := range versions {
		versionName := sanitizeEditTerminalLabel(version.VersionName)
		if versionName == "" {
			versionName = sanitizeEditTerminalLabel(version.ID)
		}

		safeID := sanitizeEditTerminalLabel(version.ID)
		display := versionName
		if safeID != versionName {
			display = fmt.Sprintf("%s (ID: %s)", versionName, safeID)
		}
		if version.DatePublished != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, version.DatePublished); parseErr == nil {
				display = fmt.Sprintf("%s • %s", display, parsed.Format("Jan 02, 2006"))
			}
		}
		if version.ID == currentVersionID {
			display = fmt.Sprintf("%s [current]", display)
		}

		options = append(options, huh.NewOption(display, version.ID))
		versionMap[version.ID] = version
	}
	options = append(options, huh.NewOption("Go back", "__back"))

	selectedID := currentVersionID
	if selectedID == "" && len(versions) > 0 {
		selectedID = versions[0].ID
	}

	err = huh.NewSelect[string]().
		Title(fmt.Sprintf("Select version for %s", slug)).
		Description("Choose a version, or go back.").
		Options(options...).
		Value(&selectedID).
		Height(pickerHeight(len(options))).
		Run()
	if err != nil {
		return nil, true, nil
	}
	if selectedID == "__back" {
		return nil, true, nil
	}

	selected, exists := versionMap[selectedID]
	if !exists {
		return nil, false, fmt.Errorf("selected version not found")
	}

	return &selected, false, nil
}

func sanitizeEditTerminalLabel(value string) string {
	cleaned := make([]rune, 0, len(value))
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' {
			cleaned = append(cleaned, ' ')
			continue
		}
		if unicode.IsPrint(r) && r != 0x1b {
			cleaned = append(cleaned, r)
		}
	}
	return string(cleaned)
}

func confirmEditApply(out cmdOutput) bool {
	confirmApply := false
	if err := huh.NewConfirm().
		Title("Apply these changes to vinth.lock.json?").
		Affirmative("Apply").
		Negative("Cancel").
		Value(&confirmApply).
		Run(); err != nil {
		out.Warn("Edit cancelled.")
		return false
	}

	if !confirmApply {
		out.Warn("No changes were applied.")
		return false
	}

	return true
}

func init() {
	rootCmd.AddCommand(editCmd)
}

func pickerHeight(optionCount int) int {
	const (
		defaultTerminalHeight = 24
		terminalBuffer        = 3
		chromeRows            = 2
		minVisibleOptions     = 3
		maxVisibleOptions     = 18
		minTotalHeight        = minVisibleOptions + chromeRows
	)

	termHeight := defaultTerminalHeight
	if fd := int(os.Stdout.Fd()); term.IsTerminal(fd) {
		if _, h, err := term.GetSize(fd); err == nil {
			termHeight = h
		}
	}

	availableHeight := termHeight - terminalBuffer
	if availableHeight < minTotalHeight {
		availableHeight = minTotalHeight
	}

	visibleOptions := optionCount
	if visibleOptions < minVisibleOptions {
		visibleOptions = minVisibleOptions
	}
	if visibleOptions > maxVisibleOptions {
		visibleOptions = maxVisibleOptions
	}

	height := visibleOptions + chromeRows
	if height > availableHeight {
		height = availableHeight
	}
	if height < minTotalHeight {
		height = minTotalHeight
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
	green := termenv.String().Foreground(t.Color("10")).Bold()
	yellow := termenv.String().Foreground(t.Color("11")).Bold()
	red := termenv.String().Foreground(t.Color("9")).Bold()
	white := termenv.String().Foreground(t.Color("15")).Bold()

	pbar, bar := newStandardProgress(len(lf.Mods), "Checking API ", green)

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
