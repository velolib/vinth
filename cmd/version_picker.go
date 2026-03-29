package cmd

import (
	"fmt"

	"github.com/charmbracelet/huh"
)

const versionPickerPageSize = 120

func selectMinecraftVersion(current string, versions []string) (string, error) {
	if len(versions) == 0 {
		return "", fmt.Errorf("no Minecraft versions available")
	}

	if len(versions) <= versionPickerPageSize {
		selected := current
		if selected == "" {
			selected = versions[0]
		}

		err := huh.NewSelect[string]().
			Title("Select Minecraft Version").
			Options(huh.NewOptions(versions...)...).
			Value(&selected).
			Height(pickerHeight(len(versions))).
			Run()
		return selected, err
	}

	pages := (len(versions) + versionPickerPageSize - 1) / versionPickerPageSize
	pageOptions := make([]huh.Option[int], 0, pages)
	selectedPage := 0

	for page := 0; page < pages; page++ {
		start := page * versionPickerPageSize
		end := start + versionPickerPageSize
		if end > len(versions) {
			end = len(versions)
		}

		if current != "" {
			for i := start; i < end; i++ {
				if versions[i] == current {
					selectedPage = page
					break
				}
			}
		}

		label := fmt.Sprintf("%d-%d: %s ... %s", start+1, end, versions[start], versions[end-1])
		pageOptions = append(pageOptions, huh.NewOption(label, page))
	}

	if err := huh.NewSelect[int]().
		Title("Select Minecraft Version Range").
		Description("Large version list detected; choose a range first for faster navigation.").
		Options(pageOptions...).
		Value(&selectedPage).
		Height(pickerHeight(len(pageOptions))).
		Run(); err != nil {
		return "", err
	}

	start := selectedPage * versionPickerPageSize
	end := start + versionPickerPageSize
	if end > len(versions) {
		end = len(versions)
	}

	subset := versions[start:end]
	selected := subset[0]
	if current != "" {
		for _, version := range subset {
			if version == current {
				selected = current
				break
			}
		}
	}

	err := huh.NewSelect[string]().
		Title("Select Minecraft Version").
		Options(huh.NewOptions(subset...)...).
		Value(&selected).
		Height(pickerHeight(len(subset))).
		Run()

	return selected, err
}
