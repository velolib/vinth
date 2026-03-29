package cmd

import (
	stderrors "errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"github.com/velolib/vinth/internal/api"
	vinthErrors "github.com/velolib/vinth/internal/errors"
	"github.com/velolib/vinth/internal/lockfile"
	"github.com/velolib/vinth/internal/utils"
)

type dependencyCheckResult struct {
	MissingByMod map[string][]string
	FetchErrors  map[string]error
}

type checkTarget struct {
	slug  string
	entry lockfile.ModEntry
}

var depsAdd bool

var depsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Check required dependencies for all mods in the lockfile",
	Long:  "Check required dependencies for mods in vinth.lock.json and optionally add missing dependencies.",
	Example: `  vinth deps
  vinth deps --add`,
	Run: func(cmd *cobra.Command, args []string) {
		out := newCmdOutput()
		t := termenv.ColorProfile()
		green := termenv.String().Foreground(t.Color("10")).Bold()
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

		out.Info(fmt.Sprintf("Checking dependencies for %d mod(s)...", len(lf.Mods)))

		result := checkDependenciesWithProgress(lf, nil, nil, t)
		printDependencySummary(result, t, false)
		out.Blank()
		out.Summary("Dependency check", metric("checked", len(lf.Mods)), metric("mods_missing_deps", len(result.MissingByMod)), metric("api_errors", len(result.FetchErrors)))

		if len(result.FetchErrors) > 0 {
			out.Blank()
			out.Error(fmt.Sprintf("Dependency check failed for %d mod(s) due to API errors.", len(result.FetchErrors)))
			os.Exit(1)
		}

		if len(result.MissingByMod) > 0 {
			out.Blank()
			out.Error("Missing dependencies found.")
			missing := uniqueMissingDependencies(result.MissingByMod)

			if depsAdd {
				out.Blank()
				out.Info(fmt.Sprintf("Adding %d missing dependency mod(s)...", len(missing)))
				out.Blank()

				var wg sync.WaitGroup
				var mu sync.Mutex
				sem := make(chan struct{}, 8)
				mpbStyle := mpb.WithWidth(40)
				pbar := mpb.New(mpbStyle)
				bar := pbar.New(int64(len(missing)),
					mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("·").Rbound("╟"),
					mpb.PrependDecorators(
						decor.Name(green.Styled("Fetching data "), decor.WC{W: 16, C: decor.DindentRight}),
						decor.CountersNoUnit("%d / %d"),
					),
					mpb.AppendDecorators(
						decor.Percentage(decor.WCSyncWidth),
					),
				)

				addedCount := 0
				existsCount := 0
				failedCount := 0
				addedSlugs := make(map[string]struct{}, len(missing))
				for _, depSlug := range missing {
					wg.Add(1)
					go func(slug string) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()

						mu.Lock()
						if _, exists := lf.Mods[slug]; exists {
							existsCount++
							statusMsg := fmt.Sprintf("⏭️  %s: %s", white.Styled(slug), green.Styled("Already exists in lockfile"))
							pbar.Write([]byte(statusMsg + "\n"))
							bar.Increment()
							mu.Unlock()
							return
						}
						mu.Unlock()

						versionInfo, fetchErr := api.FetchLatestVersion(slug, lf.GameVersion, lf.Loader)

						mu.Lock()
						defer mu.Unlock()

						var statusMsg string
						if fetchErr != nil {
							failedCount++
							statusMsg = fmt.Sprintf("⚠️  %s: failed to fetch latest version (%v)", white.Styled(slug), fetchErr)
						} else if len(versionInfo.Files) == 0 {
							failedCount++
							statusMsg = fmt.Sprintf("⚠️  %s: no files found", white.Styled(slug))
						} else {
							primaryFile := versionInfo.Files[0]
							safeFileName, sanitizeErr := utils.SanitizeModFileName(primaryFile.Filename)
							if sanitizeErr != nil {
								failedCount++
								statusMsg = fmt.Sprintf("⚠️  %s: invalid file name from API (%v)", white.Styled(slug), sanitizeErr)
							} else {
								lf.Mods[slug] = lockfile.ModEntry{
									ProjectID:   versionInfo.ProjectID,
									VersionID:   versionInfo.ID,
									FileName:    safeFileName,
									DownloadURL: primaryFile.URL,
									FileSize:    primaryFile.Size,
									Hash:        primaryFile.Hashes.Sha512,
								}
								statusMsg = fmt.Sprintf("✅ %s: Added %s", white.Styled(slug), green.Styled(safeFileName))
								addedCount++
								addedSlugs[slug] = struct{}{}
							}
						}

						pbar.Write([]byte(statusMsg + "\n"))
						bar.Increment()
					}(depSlug)
				}

				wg.Wait()
				pbar.Wait()
				out.Blank()
				out.Summary("Dependency add", metric("processed", len(missing)), metric("added", addedCount), metric("skipped", existsCount), metric("failed", failedCount))

				if addedCount > 0 {
					if saveErr := lf.Save(); saveErr != nil {
						out.Error(fmt.Sprintf("Failed to save lockfile after adding dependencies: %v", saveErr))
						os.Exit(1)
					}
					out.Blank()
					out.Success(fmt.Sprintf("Added %d dependency mod(s) to vinth.lock.json.", addedCount))
				}
				if existsCount > 0 {
					out.Blank()
					out.Info(fmt.Sprintf("Skipped %d dependency mod(s) already present in lockfile.", existsCount))
				}

				recheckTargets := collectDependencyRecheckTargets(result.MissingByMod, addedSlugs)
				remaining := checkDependenciesWithProgress(lf, nil, recheckTargets, t)
				if len(remaining.FetchErrors) > 0 {
					out.Blank()
					out.Error(fmt.Sprintf("Dependency re-check failed for %d mod(s) due to API errors.", len(remaining.FetchErrors)))
					os.Exit(1)
				}
				if len(remaining.MissingByMod) > 0 {
					stillMissing := uniqueMissingDependencies(remaining.MissingByMod)
					out.Blank()
					out.Warn(fmt.Sprintf("Some required dependencies are still missing. Run again: vinth deps --add (%s)", strings.Join(stillMissing, " ")))
					os.Exit(1)
				}

				out.Blank()
				out.Success("All required dependencies are present in vinth.lock.json.")
				return
			}

			out.Tip("Suggested command: vinth deps --add")
			os.Exit(1)
		}

		out.Success("All required dependencies are present in vinth.lock.json.")
	},
}

func init() {
	depsCmd.Flags().BoolVar(&depsAdd, "add", false, "Automatically add missing required dependencies to the lockfile")
	rootCmd.AddCommand(depsCmd)
}

func buildCheckTargets(lf *lockfile.Lockfile, targetSlugs []string) []checkTarget {
	targets := make([]checkTarget, 0, len(lf.Mods))
	if len(targetSlugs) == 0 {
		for slug, entry := range lf.Mods {
			targets = append(targets, checkTarget{slug: slug, entry: entry})
		}
		return targets
	}

	seen := make(map[string]struct{}, len(targetSlugs))
	for _, slug := range targetSlugs {
		if _, exists := seen[slug]; exists {
			continue
		}
		seen[slug] = struct{}{}
		entry, exists := lf.Mods[slug]
		if !exists {
			continue
		}
		targets = append(targets, checkTarget{slug: slug, entry: entry})
	}

	return targets
}

func prefetchDependencyProjectSlugs(targets []checkTarget, gameVersion string, loader string, versionCache map[string]*api.ModrinthVersion, result *dependencyCheckResult) (map[string]string, map[string]error) {
	projectIDs := make(map[string]struct{})
	for _, target := range targets {
		slug := target.slug
		entry := target.entry

		versionInfo, ok := versionCache[slug]
		if !ok {
			versionInfo, ok = versionCache[entry.VersionID]
			if !ok {
				var err error
				versionInfo, err = fetchDependencyCheckVersion(slug, entry, gameVersion, loader)
				if err != nil {
					result.FetchErrors[slug] = err
					continue
				}
				versionCache[entry.VersionID] = versionInfo
			}
			versionCache[slug] = versionInfo
		}

		for _, dep := range versionInfo.Dependencies {
			if dep.DependencyType == "required" && dep.ProjectID != "" {
				projectIDs[dep.ProjectID] = struct{}{}
			}
		}
	}

	projectSlugCache := make(map[string]string, len(projectIDs))
	projectSlugErrors := make(map[string]error)
	if len(projectIDs) == 0 {
		return projectSlugCache, projectSlugErrors
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 8)

	for projectID := range projectIDs {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			slug, err := api.FetchProjectSlug(pid)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				projectSlugErrors[pid] = err
				return
			}
			projectSlugCache[pid] = slug
		}(projectID)
	}

	wg.Wait()
	return projectSlugCache, projectSlugErrors
}

func fetchDependencyCheckVersion(slug string, entry lockfile.ModEntry, gameVersion string, loader string) (*api.ModrinthVersion, error) {
	versionInfo, err := api.FetchVersionByID(entry.VersionID)
	if err == nil {
		return versionInfo, nil
	}

	var appErr *vinthErrors.AppError
	if stderrors.As(err, &appErr) && appErr.Code == "notfound" {
		fallback, fallbackErr := api.FetchLatestVersion(slug, gameVersion, loader)
		if fallbackErr == nil {
			return fallback, nil
		}
	}

	return nil, err
}

func collectDependencyRecheckTargets(missingByMod map[string][]string, addedSlugs map[string]struct{}) []string {
	targetSet := make(map[string]struct{}, len(missingByMod)+len(addedSlugs))
	for slug := range missingByMod {
		targetSet[slug] = struct{}{}
	}
	for slug := range addedSlugs {
		targetSet[slug] = struct{}{}
	}

	targets := make([]string, 0, len(targetSet))
	for slug := range targetSet {
		targets = append(targets, slug)
	}
	sort.Strings(targets)
	return targets
}

func checkDependencies(lf *lockfile.Lockfile, prefetched map[string]*api.ModrinthVersion, targetSlugs []string) dependencyCheckResult {
	result := dependencyCheckResult{
		MissingByMod: make(map[string][]string),
		FetchErrors:  make(map[string]error),
	}

	installed := make(map[string]struct{}, len(lf.Mods))
	for slug := range lf.Mods {
		installed[slug] = struct{}{}
	}

	versionCache := make(map[string]*api.ModrinthVersion)
	for slug, versionInfo := range prefetched {
		if versionInfo != nil {
			versionCache[slug] = versionInfo
		}
	}

	targets := buildCheckTargets(lf, targetSlugs)
	projectSlugCache, projectSlugErrors := prefetchDependencyProjectSlugs(targets, lf.GameVersion, lf.Loader, versionCache, &result)

	for _, target := range targets {
		slug := target.slug
		versionInfo, ok := versionCache[slug]
		if !ok {
			continue
		}

		missingSet := make(map[string]struct{})
		dependencyError := false
		for _, dep := range versionInfo.Dependencies {
			if dep.DependencyType != "required" || dep.ProjectID == "" {
				continue
			}

			depSlug, ok := projectSlugCache[dep.ProjectID]
			if !ok {
				if err, exists := projectSlugErrors[dep.ProjectID]; exists {
					result.FetchErrors[slug] = err
					dependencyError = true
					break
				}
				continue
			}

			if _, exists := installed[depSlug]; !exists {
				missingSet[depSlug] = struct{}{}
			}
		}

		if dependencyError {
			continue
		}

		if len(missingSet) == 0 {
			continue
		}

		missing := make([]string, 0, len(missingSet))
		for depSlug := range missingSet {
			missing = append(missing, depSlug)
		}
		sort.Strings(missing)
		result.MissingByMod[slug] = missing
	}

	return result
}

func checkDependenciesWithProgress(lf *lockfile.Lockfile, prefetched map[string]*api.ModrinthVersion, targetSlugs []string, profile termenv.Profile) dependencyCheckResult {
	result := dependencyCheckResult{
		MissingByMod: make(map[string][]string),
		FetchErrors:  make(map[string]error),
	}

	installed := make(map[string]struct{}, len(lf.Mods))
	for slug := range lf.Mods {
		installed[slug] = struct{}{}
	}

	versionCache := make(map[string]*api.ModrinthVersion)
	for slug, versionInfo := range prefetched {
		if versionInfo != nil {
			versionCache[slug] = versionInfo
		}
	}

	targets := buildCheckTargets(lf, targetSlugs)
	projectSlugCache := make(map[string]string)

	green := termenv.String().Foreground(profile.Color("10")).Bold()
	yellow := termenv.String().Foreground(profile.Color("11")).Bold()
	red := termenv.String().Foreground(profile.Color("9")).Bold()
	white := termenv.String().Foreground(profile.Color("15")).Bold()

	mpbStyle := mpb.WithWidth(40)
	pbar := mpb.New(mpbStyle)
	bar := pbar.New(int64(len(targets)),
		mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("·").Rbound("╟"),
		mpb.PrependDecorators(
			decor.Name(green.Styled("Checking deps "), decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WCSyncWidth),
		),
	)

	for _, target := range targets {
		slug := target.slug
		entry := target.entry
		versionInfo, ok := versionCache[slug]
		if !ok {
			versionInfo, ok = versionCache[entry.VersionID]
			if !ok {
				var err error
				versionInfo, err = fetchDependencyCheckVersion(slug, entry, lf.GameVersion, lf.Loader)
				if err != nil {
					result.FetchErrors[slug] = err
					pbar.Write([]byte(fmt.Sprintf("❌ %s: %s\n", white.Styled(slug), red.Styled("Failed to fetch version metadata"))))
					bar.Increment()
					continue
				}
				versionCache[entry.VersionID] = versionInfo
			}
			versionCache[slug] = versionInfo
		}

		missingSet := make(map[string]struct{})
		dependencyError := false
		for _, dep := range versionInfo.Dependencies {
			if dep.DependencyType != "required" || dep.ProjectID == "" {
				continue
			}

			depSlug, ok := projectSlugCache[dep.ProjectID]
			if !ok {
				resolvedSlug, err := api.FetchProjectSlug(dep.ProjectID)
				if err != nil {
					result.FetchErrors[slug] = err
					dependencyError = true
					break
				}
				depSlug = resolvedSlug
				projectSlugCache[dep.ProjectID] = depSlug
			}

			if _, exists := installed[depSlug]; !exists {
				missingSet[depSlug] = struct{}{}
			}
		}

		if dependencyError {
			pbar.Write([]byte(fmt.Sprintf("❌ %s: %s\n", white.Styled(slug), red.Styled("Failed to resolve dependency metadata"))))
			bar.Increment()
			continue
		}

		if len(missingSet) == 0 {
			pbar.Write([]byte(fmt.Sprintf("✅ %s: %s\n", white.Styled(slug), green.Styled("Dependencies satisfied"))))
			bar.Increment()
			continue
		}

		missing := make([]string, 0, len(missingSet))
		for depSlug := range missingSet {
			missing = append(missing, depSlug)
		}
		sort.Strings(missing)
		result.MissingByMod[slug] = missing
		pbar.Write([]byte(fmt.Sprintf("⚠️  %s: %s\n", white.Styled(slug), yellow.Styled("Missing required dependencies"))))
		bar.Increment()
	}

	pbar.Wait()
	return result
}

func printDependencySummary(result dependencyCheckResult, profile termenv.Profile, compact bool) {
	yellow := termenv.String().Foreground(profile.Color("11")).Bold()
	red := termenv.String().Foreground(profile.Color("9")).Bold()
	white := termenv.String().Foreground(profile.Color("15")).Bold()

	if !compact && len(result.FetchErrors) > 0 {
		slugs := make([]string, 0, len(result.FetchErrors))
		for slug := range result.FetchErrors {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			fmt.Println(yellow.Styled(fmt.Sprintf("⚠️  %s: could not fetch dependency data (%v)", white.Styled(slug), result.FetchErrors[slug])))
		}
	}

	if len(result.MissingByMod) == 0 {
		return
	}

	slugs := make([]string, 0, len(result.MissingByMod))
	for slug := range result.MissingByMod {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	for _, slug := range slugs {
		fmt.Println(red.Styled(fmt.Sprintf("❌ %s missing required deps: %s", white.Styled(slug), strings.Join(result.MissingByMod[slug], ", "))))
	}
}

func uniqueMissingDependencies(missingByMod map[string][]string) []string {
	set := make(map[string]struct{})
	for _, missing := range missingByMod {
		for _, dep := range missing {
			set[dep] = struct{}{}
		}
	}
	deps := make([]string, 0, len(set))
	for dep := range set {
		deps = append(deps, dep)
	}
	sort.Strings(deps)
	return deps
}
