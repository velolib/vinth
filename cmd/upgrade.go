// cmd/upgrade.go
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

var upgradeCmd = &cobra.Command{
	Use:   "upgrade [mod-slugs...]",
	Short: "Check for and update to the latest mod versions in the lockfile",
	Long:  "Upgrade specific mods by slug, or upgrade all tracked mods if no slugs are provided.",
	Example: `  vinth upgrade
  vinth upgrade sodium
  vinth upgrade sodium lithium fabric-api`,
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()
		t := termenv.ColorProfile()
		green := termenv.String().Foreground(t.Color("10")).Bold()
		yellow := termenv.String().Foreground(t.Color("11")).Bold()
		cyan := termenv.String().Foreground(t.Color("14")).Bold()

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

		// Determine which mods to upgrade
		modsToUpgrade := make(map[string]lockfile.ModEntry)
		if len(args) == 0 {
			// If no args provided, upgrade all mods
			modsToUpgrade = lf.Mods
		} else {
			// If args provided, only upgrade those mods
			for _, slug := range args {
				if entry, exists := lf.Mods[slug]; exists {
					modsToUpgrade[slug] = entry
				} else {
					out.Warn(fmt.Sprintf("%s: Not found in lockfile", slug))
				}
			}
			if len(modsToUpgrade) == 0 {
				out.Error("No valid mods specified.")
				os.Exit(1)
			}
		}

		out.Info(fmt.Sprintf("Checking for updates for %d mod(s)...", len(modsToUpgrade)))
		out.Blank()

		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, 8)
		pbar, bar := newStandardProgress(len(modsToUpgrade), "Checking API ", green)
		upgradedCount := 0
		upToDateCount := 0
		skippedLockedCount := 0
		failedCount := 0
		white := termenv.String().Foreground(t.Color("15")).Bold()
		for slug, entry := range modsToUpgrade {
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
				sem <- struct{}{}
				defer func() { <-sem }()
				latestInfo, err := api.FetchLatestVersion(modSlug, lf.GameVersion, lf.Loader)
				mu.Lock()
				defer mu.Unlock()
				var statusMsg string
				if err != nil || len(latestInfo.Files) == 0 {
					failedCount++
					statusMsg = fmt.Sprintf("⚠️  %s: %s", white.Styled(modSlug), yellow.Styled("Failed to fetch data"))
				} else if latestInfo.ID != currentEntry.VersionID {
					primaryFile := latestInfo.Files[0]
					safeFileName, sanitizeErr := utils.SanitizeModFileName(primaryFile.Filename)
					if sanitizeErr != nil {
						failedCount++
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
						statusMsg = fmt.Sprintf("⬆️  %s: %s", white.Styled(modSlug), cyan.Styled(fmt.Sprintf("Upgraded (%s -> %s)", currentEntry.VersionID, latestInfo.ID)))
						upgradedCount++
					}
				} else {
					upToDateCount++
					statusMsg = fmt.Sprintf("✅ %s: %s", white.Styled(modSlug), green.Styled("Already up to date"))
				}
				pbar.Write([]byte(statusMsg + "\n"))
				bar.Increment()
			}(slug, entry)
		}
		wg.Wait()
		pbar.Wait()
		out.Blank()
		out.Summary("Upgrade", metric("checked", len(modsToUpgrade)), metric("upgraded", upgradedCount), metric("up_to_date", upToDateCount), metric("skipped_locked", skippedLockedCount), metric("failed", failedCount))

		if upgradedCount > 0 {
			if err := lf.Save(); err != nil {
				out.Error(fmt.Sprintf("Failed to save lockfile: %v", err))
				os.Exit(1)
			}
			out.Blank()
			out.Success(fmt.Sprintf("Upgraded %d mod(s) in vinth.lock.json.", upgradedCount))
			out.Tip("Run 'vinth sync' to apply changes to your files.")
		} else {
			out.Blank()
			if skippedLockedCount > 0 {
				out.Success("No upgrades applied. Selected mods are either already up to date or version-locked.")
			} else {
				out.Success("All specified mods are already up to date.")
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
}
