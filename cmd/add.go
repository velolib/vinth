// cmd/add.go
package cmd

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"

	"github.com/spf13/cobra"
	"github.com/velolib/vinth/internal/api"
	"github.com/velolib/vinth/internal/lockfile"
	"github.com/velolib/vinth/internal/utils"
)

var addByID bool
var addLock bool
var addLatest bool

var addCmd = &cobra.Command{
	Use:   "add [mod-identifiers...]",
	Short: "Add one or more mods to the lockfile concurrently",
	Long:  "Add mods to vinth.lock.json by slug (default) or by Modrinth project ID with --id. Interactively select versions for each mod, or use --latest to automatically select the latest compatible version.",
	Example: `  vinth add sodium fabric-api iris
	vinth add --lock sodium iris
	vinth add --latest sodium iris
  vinth add --id AANobbMI P7dR8mSH
  vinth add --modrinth-id AANobbMI`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()
		t := termenv.ColorProfile()
		green := termenv.String().Foreground(t.Color("10")).Bold()
		yellow := termenv.String().Foreground(t.Color("11")).Bold()
		red := termenv.String().Foreground(t.Color("9")).Bold()

		if !lockfile.Exists() {
			out.Warn("No vinth.lock.json found. Creating one first.")
			RunCreateWizard()
		}

		lf, err := lockfile.Load()
		if err != nil {
			out.Error(fmt.Sprintf("Failed to read lockfile: %v", err))
			os.Exit(1)
		}

		if addByID {
			out.Info(fmt.Sprintf("Adding %d mod(s) by Modrinth project ID...", len(args)))
		} else {
			out.Info(fmt.Sprintf("Adding %d mod(s) by slug...", len(args)))
		}
		if addLock {
			out.Info("Version locking is enabled for this add operation.")
		}
		if addLatest {
			out.Info("Using latest versions (--latest mode).")
		} else {
			out.Info("Interactive version selection enabled for each mod.")
		}
		out.Blank()

		// Phase 1: Resolve slugs and check if mods already exist
		type resolvedMod struct {
			identifier  string
			slug        string
			displayName string
		}

		resolvedMods := make([]resolvedMod, 0)
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		inProgress := make(map[string]struct{})
		existsCount := 0
		failedCount := 0

		pbar, bar := newStandardProgress(len(args), "Resolving slugs ", green)
		white := termenv.String().Foreground(t.Color("15")).Bold()

		for _, identifier := range args {
			wg.Add(1)
			go func(modIdentifier string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				modSlug := modIdentifier
				if addByID {
					resolvedSlug, resolveErr := api.FetchProjectSlug(modIdentifier)
					if resolveErr != nil {
						mu.Lock()
						failedCount++
						mu.Unlock()
						statusMsg := fmt.Sprintf("❌ %s: %s", white.Styled(modIdentifier), red.Styled(fmt.Sprintf("Failed to resolve project ID (%v)", resolveErr)))
						pbar.Write([]byte(statusMsg + "\n"))
						bar.Increment()
						return
					}
					modSlug = resolvedSlug
				}

				displayName := modSlug
				if addByID && modIdentifier != modSlug {
					displayName = fmt.Sprintf("%s -> %s", modIdentifier, modSlug)
				}

				mu.Lock()
				if _, exists := lf.Mods[modSlug]; exists {
					existsCount++
					statusMsg := fmt.Sprintf("⏭️  %s: %s", white.Styled(displayName), yellow.Styled("Already exists in lockfile"))
					pbar.Write([]byte(statusMsg + "\n"))
					bar.Increment()
					mu.Unlock()
					return
				}
				if _, exists := inProgress[modSlug]; exists {
					existsCount++
					statusMsg := fmt.Sprintf("⏭️  %s: %s", white.Styled(displayName), yellow.Styled("Already requested in this run"))
					pbar.Write([]byte(statusMsg + "\n"))
					bar.Increment()
					mu.Unlock()
					return
				}
				inProgress[modSlug] = struct{}{}
				mu.Unlock()

				mu.Lock()
				resolvedMods = append(resolvedMods, resolvedMod{
					identifier:  modIdentifier,
					slug:        modSlug,
					displayName: displayName,
				})
				statusMsg := fmt.Sprintf("✅ %s: %s", white.Styled(displayName), green.Styled("Resolved"))
				pbar.Write([]byte(statusMsg + "\n"))
				bar.Increment()
				mu.Unlock()
			}(identifier)
		}

		wg.Wait()
		pbar.Wait()
		sort.Slice(resolvedMods, func(i, j int) bool { return resolvedMods[i].displayName < resolvedMods[j].displayName })

		if len(resolvedMods) == 0 {
			out.Blank()
			out.Summary("Add", metric("processed", len(args)), metric("added", 0), metric("skipped", existsCount), metric("failed", failedCount))
			if existsCount > 0 {
				out.Warn(fmt.Sprintf("No new mods were added. %d mod(s) already exist.", existsCount))
			} else {
				out.Warn("No mods were added to the lockfile.")
			}
			return
		}

		// Phase 2: Get versions (either latest or interactive selection)
		out.Blank()
		type selectedModVersion struct {
			slug    string
			display string
			version *api.ModrinthVersion
			err     error
		}

		selectedVersions := make([]selectedModVersion, 0)
		prefetchedVersions := make(map[string]*api.ModrinthVersion)
		successCount := 0

		if addLatest {
			// Fast concurrent mode: fetch latest versions
			out.Info("Fetching latest versions...")
			pbar, bar := newStandardProgress(len(resolvedMods), "Fetching data ", green)

			for _, resolved := range resolvedMods {
				wg.Add(1)
				go func(mod resolvedMod) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					versionInfo, err := api.FetchLatestVersion(mod.slug, lf.GameVersion, lf.Loader)

					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						selectedVersions = append(selectedVersions, selectedModVersion{
							slug:    mod.slug,
							display: mod.displayName,
							version: nil,
							err:     err,
						})
						statusMsg := fmt.Sprintf("❌ %s: %s", white.Styled(mod.displayName), red.Styled(fmt.Sprintf("Failed (%v)", err)))
						pbar.Write([]byte(statusMsg + "\n"))
						bar.Increment()
					} else if len(versionInfo.Files) == 0 {
						selectedVersions = append(selectedVersions, selectedModVersion{
							slug:    mod.slug,
							display: mod.displayName,
							version: nil,
							err:     fmt.Errorf("no files found"),
						})
						statusMsg := fmt.Sprintf("⚠️  %s: %s", white.Styled(mod.displayName), yellow.Styled("No files found"))
						pbar.Write([]byte(statusMsg + "\n"))
						bar.Increment()
					} else {
						prefetchedVersions[mod.slug] = versionInfo
						selectedVersions = append(selectedVersions, selectedModVersion{
							slug:    mod.slug,
							display: mod.displayName,
							version: versionInfo,
							err:     nil,
						})
						statusMsg := fmt.Sprintf("✅ %s", white.Styled(mod.displayName))
						pbar.Write([]byte(statusMsg + "\n"))
						bar.Increment()
					}
				}(resolved)
			}

			wg.Wait()
			pbar.Wait()
		} else {
			// Interactive mode: fetch and show dialogues serially for clean terminal
			out.Info("Preparing version selection...")
			out.Blank()

			for i, resolved := range resolvedMods {
				out.Info(fmt.Sprintf("Fetching versions for %s (%d/%d)...", resolved.displayName, i+1, len(resolvedMods)))
				versionInfo, cancelled, err := selectVersionForMod(resolved.slug, lf.GameVersion, lf.Loader, out)
				if cancelled {
					out.Warn("Add cancelled. No changes were applied.")
					return
				}

				if err != nil {
					selectedVersions = append(selectedVersions, selectedModVersion{
						slug:    resolved.slug,
						display: resolved.displayName,
						version: nil,
						err:     err,
					})
				} else if len(versionInfo.Files) == 0 {
					selectedVersions = append(selectedVersions, selectedModVersion{
						slug:    resolved.slug,
						display: resolved.displayName,
						version: nil,
						err:     fmt.Errorf("no files found"),
					})
				} else {
					prefetchedVersions[resolved.slug] = versionInfo
					selectedVersions = append(selectedVersions, selectedModVersion{
						slug:    resolved.slug,
						display: resolved.displayName,
						version: versionInfo,
						err:     nil,
					})
				}
			}
			out.Blank()
		}

		// Phase 3: Process results and create ModEntry for each selected version
		for _, selected := range selectedVersions {
			var statusMsg string
			if selected.err != nil {
				failedCount++
				statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(selected.display), red.Styled(fmt.Sprintf("Failed (%v)", selected.err)))
				fmt.Printf("%s\n", statusMsg)
			} else if selected.version != nil {
				primaryFile := selected.version.Files[0]
				safeFileName, sanitizeErr := utils.SanitizeModFileName(primaryFile.Filename)
				if sanitizeErr != nil {
					failedCount++
					statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(selected.display), red.Styled(fmt.Sprintf("Invalid file name from API (%v)", sanitizeErr)))
					fmt.Printf("%s\n", statusMsg)
				} else {
					versionName := selected.version.VersionName
					if versionName == "" {
						versionName = selected.version.ID
					}
					lf.Mods[selected.slug] = lockfile.ModEntry{
						ProjectID:   selected.version.ProjectID,
						VersionID:   selected.version.ID,
						VersionName: versionName,
						VersionLock: addLock,
						FileName:    safeFileName,
						DownloadURL: primaryFile.URL,
						FileSize:    primaryFile.Size,
						Hash:        primaryFile.Hashes.Sha512,
					}
					statusMsg = fmt.Sprintf("✅ %s: %s", white.Styled(selected.display), green.Styled(fmt.Sprintf("Added %s", safeFileName)))
					fmt.Printf("%s\n", statusMsg)
					successCount++
				}
			}
		}

		out.Blank()
		out.Summary("Add", metric("processed", len(args)), metric("added", successCount), metric("skipped", existsCount), metric("failed", failedCount))

		if successCount > 0 {
			if err := lf.Save(); err != nil {
				out.Error(fmt.Sprintf("Failed to save lockfile: %v", err))
				os.Exit(1)
			}
			out.Blank()
			out.Success(fmt.Sprintf("Updated vinth.lock.json with %d mod(s).", successCount))

			out.Blank()
			out.Info("Checking dependencies...")
			addedSlugs := make([]string, 0, len(prefetchedVersions))
			for slug := range prefetchedVersions {
				addedSlugs = append(addedSlugs, slug)
			}
			sort.Strings(addedSlugs)
			depResult := checkDependencies(lf, prefetchedVersions, addedSlugs)
			printDependencySummary(depResult, t, true)
			if len(depResult.MissingByMod) > 0 {
				out.Blank()
				out.Tip("Missing required dependencies detected. Run: vinth deps --add")
			} else {
				out.Success("No missing required dependencies found.")
			}
			if len(depResult.FetchErrors) > 0 {
				out.Warn("Some dependency checks were skipped due to API errors.")
			}
		} else {
			out.Blank()
			if existsCount > 0 {
				out.Warn(fmt.Sprintf("No new mods were added. %d mod(s) already exist.", existsCount))
			} else {
				out.Warn("No mods were added to the lockfile.")
			}
		}
	},
}

func init() {
	addCmd.Flags().BoolVar(&addByID, "id", false, "Treat all arguments as Modrinth project IDs")
	addCmd.Flags().BoolVar(&addByID, "modrinth-id", false, "Treat all arguments as Modrinth project IDs")
	addCmd.Flags().BoolVar(&addLock, "lock", false, "Lock added mods to their current version so upgrade skips them")
	addCmd.Flags().BoolVar(&addLatest, "latest", false, "Skip version selection and auto-add all mods with their latest compatible versions")
	rootCmd.AddCommand(addCmd)
}

func selectVersionForMod(modSlug string, gameVersion string, loader string, out cmdOutput) (*api.ModrinthVersion, bool, error) {
	versions, err := api.FetchProjectVersions(modSlug, gameVersion, loader)
	if err != nil {
		return nil, false, err
	}

	if len(versions) == 0 {
		return nil, false, fmt.Errorf("no compatible versions found")
	}

	if len(versions) == 1 {
		return &versions[0], false, nil
	}

	// Build options for the dialogue
	options := make([]huh.Option[string], 0, len(versions))
	versionMap := make(map[string]api.ModrinthVersion)

	for _, v := range versions {
		versionName := sanitizeTerminalLabel(v.VersionName)
		if versionName == "" {
			versionName = sanitizeTerminalLabel(v.ID)
		}

		// Format the date
		dateStr := ""
		if v.DatePublished != "" {
			if t, err := time.Parse(time.RFC3339, v.DatePublished); err == nil {
				dateStr = t.Format("Jan 02, 2006")
			}
		}

		safeID := sanitizeTerminalLabel(v.ID)
		displayLabel := versionName
		if safeID != versionName {
			displayLabel = fmt.Sprintf("%s (ID: %s)", versionName, safeID)
		}
		if dateStr != "" {
			displayLabel = fmt.Sprintf("%s • %s", displayLabel, dateStr)
		}

		options = append(options, huh.NewOption(displayLabel, v.ID))
		versionMap[v.ID] = v
	}

	var selectedID string
	height := pickerHeight(len(options))
	err = huh.NewSelect[string]().
		Title(fmt.Sprintf("Select version for %s", modSlug)).
		Description("Choose which version to add for this mod.").
		Options(options...).
		Value(&selectedID).
		Height(height).
		Run()

	if err != nil {
		return nil, true, nil
	}

	selected, exists := versionMap[selectedID]
	if !exists {
		return nil, false, fmt.Errorf("selected version not found")
	}

	return &selected, false, nil
}

func sanitizeTerminalLabel(value string) string {
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
