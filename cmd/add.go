// cmd/add.go
package cmd

import (
	"fmt"
	"os"
	"sync"

	"github.com/muesli/termenv"

	"github.com/spf13/cobra"
	"github.com/velolib/vinth/internal/api"
	"github.com/velolib/vinth/internal/lockfile"
	"github.com/velolib/vinth/internal/utils"
)

var addByID bool
var addLock bool

var addCmd = &cobra.Command{
	Use:   "add [mod-identifiers...]",
	Short: "Add one or more mods to the lockfile concurrently",
	Long:  "Add mods to vinth.lock.json by slug (default) or by Modrinth project ID with --id.",
	Example: `  vinth add sodium fabric-api iris
	vinth add --lock sodium iris
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
		out.Blank()

		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, 8)
		pbar, bar := newStandardProgress(len(args), "Fetching data ", green)
		successCount := 0
		existsCount := 0
		failedCount := 0
		prefetchedVersions := make(map[string]*api.ModrinthVersion)
		inProgress := make(map[string]struct{})

		for _, identifier := range args {
			wg.Add(1)
			go func(modIdentifier string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				white := termenv.String().Foreground(t.Color("15")).Bold()
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

				versionInfo, err := api.FetchLatestVersion(modSlug, lf.GameVersion, lf.Loader)
				mu.Lock()
				defer mu.Unlock()
				delete(inProgress, modSlug)
				var statusMsg string
				if err != nil {
					failedCount++
					statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(displayName), red.Styled(fmt.Sprintf("Failed (%v)", err)))
				} else if len(versionInfo.Files) == 0 {
					failedCount++
					statusMsg = fmt.Sprintf("⚠️  %s: %s", white.Styled(displayName), yellow.Styled("No files found"))
				} else {
					primaryFile := versionInfo.Files[0]
					safeFileName, sanitizeErr := utils.SanitizeModFileName(primaryFile.Filename)
					if sanitizeErr != nil {
						failedCount++
						statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(displayName), red.Styled(fmt.Sprintf("Invalid file name from API (%v)", sanitizeErr)))
					} else {
						versionName := versionInfo.VersionName
						if versionName == "" {
							versionName = versionInfo.ID
						}
						lf.Mods[modSlug] = lockfile.ModEntry{
							ProjectID:   versionInfo.ProjectID,
							VersionID:   versionInfo.ID,
							VersionName: versionName,
							VersionLock: addLock,
							FileName:    safeFileName,
							DownloadURL: primaryFile.URL,
							FileSize:    primaryFile.Size,
							Hash:        primaryFile.Hashes.Sha512,
						}
						prefetchedVersions[modSlug] = versionInfo
						statusMsg = fmt.Sprintf("✅ %s: %s", white.Styled(displayName), green.Styled(fmt.Sprintf("Added %s", safeFileName)))
						successCount++
					}
				}
				pbar.Write([]byte(statusMsg + "\n"))
				bar.Increment()
			}(identifier)
		}

		wg.Wait()
		pbar.Wait()
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
	rootCmd.AddCommand(addCmd)
}
