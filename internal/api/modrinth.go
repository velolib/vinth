// internal/api/modrinth.go

package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/velolib/vinth/internal/errors"
)

// ModrinthVersion represents the JSON response from the Modrinth API
type ModrinthVersion struct {
	ID           string               `json:"id"`
	ProjectID    string               `json:"project_id"`
	Dependencies []ModrinthDependency `json:"dependencies"`
	Files        []struct {
		Filename string `json:"filename"`
		URL      string `json:"url"`
		Size     int64  `json:"size"`
		Hashes   struct {
			Sha512 string `json:"sha512"`
		} `json:"hashes"`
	} `json:"files"`
}

type ModrinthDependency struct {
	VersionID      string `json:"version_id"`
	ProjectID      string `json:"project_id"`
	FileName       string `json:"file_name"`
	DependencyType string `json:"dependency_type"`
}

type ModrinthProject struct {
	Slug string `json:"slug"`
}

// FetchVersionByID retrieves an exact Modrinth version by its version ID.
func FetchVersionByID(versionID string) (*ModrinthVersion, error) {
	apiURL := fmt.Sprintf("https://api.modrinth.com/v2/version/%s", versionID)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, errors.New("modrinth.FetchVersionByID", "network", err)
	}

	setAPIUserAgent(req)

	resp, err := doWith429Retry(req, 3)
	if err != nil {
		return nil, errors.New("modrinth.FetchVersionByID", "network", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, errors.New("modrinth.FetchVersionByID", "notfound", fmt.Errorf("version not found: %s", versionID))
		}
		return nil, errors.New("modrinth.FetchVersionByID", "network", fmt.Errorf("modrinth API returned status: %d", resp.StatusCode))
	}

	var version ModrinthVersion
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return nil, errors.New("modrinth.FetchVersionByID", "decode", err)
	}

	if version.ID == "" {
		return nil, errors.New("modrinth.FetchVersionByID", "decode", fmt.Errorf("empty version payload for: %s", versionID))
	}

	return &version, nil
}

// FetchLatestVersion calls the Modrinth API to get the newest file for a mod
func FetchLatestVersion(slug string, gameVersion string, loader string) (*ModrinthVersion, error) {
	apiURL := fmt.Sprintf("https://api.modrinth.com/v2/project/%s/version", slug)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, errors.New("modrinth.FetchLatestVersion", "network", err)
	}

	setAPIUserAgent(req)

	// Handle Quilt's Fabric Compatibility
	var loaderQuery string
	if loader == "quilt" {
		loaderQuery = `["quilt", "fabric"]`
	} else {
		loaderQuery = fmt.Sprintf(`["%s"]`, loader)
	}

	q := req.URL.Query()
	q.Add("game_versions", fmt.Sprintf(`["%s"]`, gameVersion))
	q.Add("loaders", loaderQuery)
	req.URL.RawQuery = q.Encode()

	// Rate Limit Retry
	resp, err := doWith429Retry(req, 3)
	if err != nil {
		return nil, errors.New("modrinth.FetchLatestVersion", "network", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, errors.New("modrinth.FetchLatestVersion", "notfound", fmt.Errorf("mod not found: %s", slug))
		}
		return nil, errors.New("modrinth.FetchLatestVersion", "network", fmt.Errorf("modrinth API returned status: %d", resp.StatusCode))
	}

	var versions []ModrinthVersion
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, errors.New("modrinth.FetchLatestVersion", "decode", err)
	}

	if len(versions) == 0 {
		return nil, errors.New("modrinth.FetchLatestVersion", "notfound", fmt.Errorf("no versions found for mod '%s' on %s for %s", slug, loader, gameVersion))
	}

	// TODO: Prefer explicit version ordering (for example published/version_number)
	// instead of relying on API response order if Modrinth changes sorting behavior.
	return &versions[0], nil
}

// FetchProjectSlug resolves a Modrinth project ID to its slug.
func FetchProjectSlug(projectID string) (string, error) {
	apiURL := fmt.Sprintf("https://api.modrinth.com/v2/project/%s", projectID)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return "", errors.New("modrinth.FetchProjectSlug", "network", err)
	}

	setAPIUserAgent(req)

	resp, err := doWith429Retry(req, 3)
	if err != nil {
		return "", errors.New("modrinth.FetchProjectSlug", "network", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return "", errors.New("modrinth.FetchProjectSlug", "notfound", fmt.Errorf("project not found: %s", projectID))
		}
		return "", errors.New("modrinth.FetchProjectSlug", "network", fmt.Errorf("modrinth API returned status: %d", resp.StatusCode))
	}

	var project ModrinthProject
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return "", errors.New("modrinth.FetchProjectSlug", "decode", err)
	}

	if project.Slug == "" {
		return "", errors.New("modrinth.FetchProjectSlug", "notfound", fmt.Errorf("project slug missing for: %s", projectID))
	}

	return project.Slug, nil
}
