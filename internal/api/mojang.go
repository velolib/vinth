// internal/api/mojang.go
package api

import (
	"encoding/json"
)

type MojangManifest struct {
	Versions []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"versions"`
}

// FetchMinecraftVersions hits the Mojang API and returns a list of version strings
func FetchMinecraftVersions(onlyReleases bool) ([]string, error) {
	resp, err := sharedHTTPClient.Get("https://launchermeta.mojang.com/mc/game/version_manifest_v2.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var manifest MojangManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}

	var versions []string
	for _, v := range manifest.Versions {
		// If the user only wants releases, skip snapshots, betas, and alphas
		if onlyReleases && v.Type != "release" {
			continue
		}
		versions = append(versions, v.ID)
	}

	return versions, nil
}
