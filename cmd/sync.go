// cmd/sync.go
package cmd

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"

	"github.com/velolib/vinth/internal/errors"

	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"github.com/velolib/vinth/internal/download"
	"github.com/velolib/vinth/internal/lockfile"
	"github.com/velolib/vinth/internal/utils"
)

var noPrune bool
var syncYes bool

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync the current directory to match vinth.lock.json",
	Long:  "Download missing/changed mods from vinth.lock.json, copy local .jar mods from ./local, and optionally prune untracked .jar files in the current directory.",
	Example: `  vinth sync
  vinth sync --no-prune
  vinth sync --yes`,
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()
		p := termenv.ColorProfile()
		bold := termenv.String().Bold()
		green := termenv.String().Foreground(p.Color("10")).Bold()
		yellow := termenv.String().Foreground(p.Color("11")).Bold()
		red := termenv.String().Foreground(p.Color("9")).Bold()
		white := termenv.String().Foreground(p.Color("15")).Bold()

		if !lockfile.Exists() {
			out.Error("No vinth.lock.json found. Run 'vinth create' first.")
			os.Exit(1)
		}

		lf, err := lockfile.Load()
		if err != nil {
			out.Error(fmt.Sprintf("Failed to read lockfile: %v", err))
			os.Exit(1)
		}

		downloadErrors := int32(0)
		downloadedCount := int32(0)
		skippedCount := int32(0)
		localCopyErrors := 0
		localModFiles := make([]string, 0)
		if len(lf.Mods) > 0 {
			out.Info(fmt.Sprintf("Syncing %d mod(s) for Minecraft %s (%s) into current directory.", len(lf.Mods), lf.GameVersion, lf.Loader))
			out.Blank()

			var wg sync.WaitGroup
			sem := make(chan struct{}, 10)
			mpbStyle := mpb.WithWidth(40)
			pbar := mpb.New(mpbStyle)
			bar := pbar.New(int64(len(lf.Mods)),
				mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("·").Rbound("╟"),
				mpb.PrependDecorators(
					decor.Name(green.Styled("Downloading "), decor.WC{W: 16, C: decor.DindentRight}),
					decor.CountersNoUnit(bold.Styled("%d / %d")),
				),
				mpb.AppendDecorators(
					decor.Percentage(decor.WCSyncWidth),
				),
			)
			for slug, entry := range lf.Mods {
				wg.Add(1)
				go func(modSlug string, modEntry lockfile.ModEntry) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					destPath, sanitizeErr := utils.SanitizeModFileName(modEntry.FileName)
					if sanitizeErr != nil {
						atomic.AddInt32(&downloadErrors, 1)
						statusMsg := fmt.Sprintf("❌ %s: %s", white.Styled(modSlug), red.Styled(fmt.Sprintf("Invalid lockfile file_name (%v)", sanitizeErr)))
						pbar.Write([]byte(statusMsg + "\n"))
						bar.Increment()
						return
					}

					var statusMsg string
					needsDownload := true
					if fi, err := os.Stat(destPath); err == nil && fi.Mode().IsRegular() {
						if modEntry.FileSize > 0 && fi.Size() != modEntry.FileSize {
							statusMsg = fmt.Sprintf("🔄 %s: %s", white.Styled(modSlug), yellow.Styled("Size mismatch, re-downloading..."))
						} else {
							// File exists, check hash when size is unknown or matches expected size.
							func() {
								f, err := os.Open(destPath)
								if err == nil {
									defer f.Close()
									hasher := sha512.New()
									if _, err := io.Copy(hasher, f); err == nil {
										actualHash := hex.EncodeToString(hasher.Sum(nil))
										if actualHash == modEntry.Hash {
											statusMsg = fmt.Sprintf("⏭️  %s: %s", white.Styled(modSlug), yellow.Styled("Skipped (Already exists, hash OK)"))
											atomic.AddInt32(&skippedCount, 1)
											needsDownload = false
										} else {
											statusMsg = fmt.Sprintf("🔄 %s: %s", white.Styled(modSlug), yellow.Styled("Hash mismatch, re-downloading..."))
										}
									} else {
										statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(modSlug), red.Styled("Failed to check hash, re-downloading..."))
									}
								} else {
									statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(modSlug), red.Styled("Failed to open file, re-downloading..."))
								}
							}()
						}
					}
					if needsDownload {
						err := download.SecureFile(modEntry.DownloadURL, destPath, modEntry.Hash)
						if err != nil {
							atomic.AddInt32(&downloadErrors, 1)
							statusMsg = fmt.Sprintf("❌ %s: %s", white.Styled(modSlug), red.Styled(fmt.Sprintf("Failed (%s)", errors.UserMessage(err))))
						} else {
							atomic.AddInt32(&downloadedCount, 1)
							statusMsg = fmt.Sprintf("✅ %s: %s", white.Styled(modSlug), green.Styled("Synced"))
						}
					}
					pbar.Write([]byte(statusMsg + "\n"))
					bar.Increment()
				}(slug, entry)
			}
			wg.Wait()
			pbar.Wait()
			out.Blank()
			out.Summary("Sync downloads", metric("processed", len(lf.Mods)), metric("downloaded", int(downloadedCount)), metric("skipped", int(skippedCount)), metric("failed", int(downloadErrors)))
		} else {
			out.Warn("Lockfile is empty. No mods to download.")
		}

		out.Blank()
		out.Info("Copying local mods from ./local (non-recursive)...")
		copiedLocalCount := 0
		localModFiles, copiedLocalCount, localCopyErrors, localCopyErr := copyLocalMods("local", ".")
		out.Summary("Sync local mods", metric("found", len(localModFiles)), metric("copied", copiedLocalCount), metric("failed", localCopyErrors))
		if len(localModFiles) == 0 {
			out.Tip("No local .jar files found in ./local.")
		}
		if localCopyErr != nil {
			out.Warn(fmt.Sprintf("Local mod copy encountered an error: %v", localCopyErr))
		}

		pruneErrors := 0
		prunedCount := 0
		pruneCandidates := 0
		pruneCancelled := false
		if noPrune {
			out.Blank()
			out.Tip("Pruning skipped (--no-prune).")
		} else {
			trackedFiles := make(map[string]struct{}, len(lf.Mods)+len(localModFiles))
			for slug, entry := range lf.Mods {
				safeName, sanitizeErr := utils.SanitizeModFileName(entry.FileName)
				if sanitizeErr != nil {
					out.Warn(fmt.Sprintf("Skipping invalid lockfile file_name for %s during prune tracking: %v", slug, sanitizeErr))
					continue
				}
				trackedFiles[safeName] = struct{}{}
			}
			for _, localModFile := range localModFiles {
				trackedFiles[localModFile] = struct{}{}
			}

			entries, err := os.ReadDir(".")
			if err != nil {
				out.Error(fmt.Sprintf("Failed to read current directory for pruning: %v", err))
				os.Exit(1)
			}

			out.Blank()
			out.Info("Pruning files not present in vinth.lock.json...")
			orphanedFiles := make([]string, 0)
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jar") {
					continue
				}
				if _, tracked := trackedFiles[entry.Name()]; tracked {
					continue
				}
				orphanedFiles = append(orphanedFiles, entry.Name())
			}

			if len(orphanedFiles) == 0 {
				out.Info("No untracked .jar files found to prune.")
			} else {
				pruneCandidates = len(orphanedFiles)
				out.Warn(fmt.Sprintf("Found %d untracked .jar file(s):", len(orphanedFiles)))
				for _, fileName := range orphanedFiles {
					fmt.Println(white.Styled(fmt.Sprintf("  • %s", fileName)))
				}

				confirmPrune := syncYes
				if !syncYes {
					if err := huh.NewConfirm().
						Title(fmt.Sprintf("Delete %d untracked .jar file(s)?", len(orphanedFiles))).
						Affirmative("Delete").
						Negative("Cancel").
						Value(&confirmPrune).
						Run(); err != nil {
						out.Warn("Pruning cancelled.")
						confirmPrune = false
					}
				}

				if !confirmPrune {
					pruneCancelled = true
					out.Tip("No files were pruned.")
				} else {
					prunePbar := mpb.New(mpb.WithWidth(40))
					pruneBar := prunePbar.New(int64(len(orphanedFiles)),
						mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("·").Rbound("╟"),
						mpb.PrependDecorators(
							decor.Name(green.Styled("Pruning files "), decor.WC{W: 16, C: decor.DindentRight}),
							decor.CountersNoUnit(bold.Styled("%d / %d")),
						),
						mpb.AppendDecorators(
							decor.Percentage(decor.WCSyncWidth),
						),
					)

					for _, fileName := range orphanedFiles {
						if err := os.Remove(fileName); err != nil {
							pruneErrors++
							fmt.Println(red.Styled(fmt.Sprintf("❌ Failed to prune %s: %v", fileName, err)))
						} else {
							prunedCount++
							fmt.Println(green.Styled(fmt.Sprintf("🗑️  Pruned: %s", fileName)))
						}
						pruneBar.Increment()
					}
					prunePbar.Wait()
				}
			}

			out.Blank()
			out.Summary("Sync prune", metric("candidates", pruneCandidates), metric("pruned", prunedCount), metric("failed", pruneErrors), metric("cancelled", boolToInt(pruneCancelled)))
		}

		totalErrors := int(downloadErrors) + pruneErrors + localCopyErrors
		out.Blank()
		if totalErrors > 0 {
			out.Warn(fmt.Sprintf("Sync finished with %d error(s).", totalErrors))
			if prunedCount > 0 {
				out.Info(fmt.Sprintf("Pruned %d file(s).", prunedCount))
			}
			os.Exit(1)
		}

		if !noPrune {
			out.Success(fmt.Sprintf("Sync complete. Pruned %d file(s).", prunedCount))
		} else {
			out.Success("Sync complete.")
		}
	},
}

func init() {
	syncCmd.Flags().BoolVar(&noPrune, "no-prune", false, "Do not remove .jar files that are not present in the lockfile")
	syncCmd.Flags().BoolVar(&syncYes, "yes", false, "Skip prune confirmation prompt")
	rootCmd.AddCommand(syncCmd)
}

func copyLocalMods(sourceDir string, destinationDir string) ([]string, int, int, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, 0, 0, nil
		}
		return []string{}, 0, 1, err
	}

	localModFiles := make([]string, 0)
	copiedCount := 0
	failedCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fileName := entry.Name()
		if !strings.HasSuffix(strings.ToLower(fileName), ".jar") {
			continue
		}

		localModFiles = append(localModFiles, fileName)

		sourcePath := filepath.Join(sourceDir, fileName)
		destinationPath := filepath.Join(destinationDir, fileName)
		if err := overwriteFile(sourcePath, destinationPath); err != nil {
			failedCount++
			continue
		}

		copiedCount++
	}

	return localModFiles, copiedCount, failedCount, nil
}

func overwriteFile(sourcePath string, destinationPath string) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.Create(destinationPath)
	if err != nil {
		return err
	}

	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		destinationFile.Close()
		return err
	}

	return destinationFile.Close()
}
