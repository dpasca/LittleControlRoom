package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
)

type codexArtifactPickerType int

const (
	codexArtifactPickerAll codexArtifactPickerType = iota
	codexArtifactPickerImages
	codexArtifactPickerVideos
	codexArtifactPickerDocuments
	codexArtifactPickerFolders
	codexArtifactPickerTypeCount
)

func (kind codexArtifactPickerType) label() string {
	switch kind {
	case codexArtifactPickerImages:
		return "Images"
	case codexArtifactPickerVideos:
		return "Videos"
	case codexArtifactPickerDocuments:
		return "Docs"
	case codexArtifactPickerFolders:
		return "Folders"
	default:
		return "All"
	}
}

func (kind codexArtifactPickerType) matches(target codexArtifactOpenTarget) bool {
	switch kind {
	case codexArtifactPickerImages:
		return target.Kind == "image"
	case codexArtifactPickerVideos:
		return target.Kind == "video"
	case codexArtifactPickerDocuments:
		return target.Kind == "doc" || target.Kind == "pdf" || target.Kind == "html" || target.Kind == "text"
	case codexArtifactPickerFolders:
		return target.Kind == "dir"
	default:
		return true
	}
}

func (picker *codexArtifactPickerState) activeTargets() []codexArtifactOpenTarget {
	if picker.ShowLatest {
		return picker.LatestTargets
	}
	return picker.Targets
}

// Keep a separate latest-reply list so opening the picker does not have to
// wait for earlier transcript chunks or depend on the viewport's scroll position.
// Parsing is memory-only; inferred paths still require the background scan.
func (picker *codexArtifactPickerState) updateLatestReply(entries []codexapp.TranscriptEntry, scanned []codexArtifactOpenTarget, complete bool) {
	latest := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == codexapp.TranscriptAgent && strings.TrimSpace(entries[i].Text) != "" {
			latest = i
			break
		}
	}
	if latest < 0 {
		picker.LatestEntry = -1
		picker.LatestReply = codexapp.TranscriptEntry{}
		picker.LatestTargets = nil
		picker.LatestHasLinks = false
		return
	}
	if picker.LatestEntry != latest || !codexTranscriptEntryEqual(picker.LatestReply, entries[latest]) {
		picker.LatestEntry = latest
		picker.LatestReply = entries[latest]
		text := codexFullTranscriptEntryLinkScanText(entries[latest])
		candidates := codexArtifactOpenTargetsFromMarkdownPrefixInProjectWithMentions(text, len(text), picker.ProjectPath, true, true, true)
		picker.LatestHasLinks = len(candidates) > 0
		picker.LatestTargets = confirmedCodexArtifactOpenTargets(locateCodexArtifactOpenTargets(candidates, latest))
	}
	var latestScanned []codexArtifactOpenTarget
	for _, target := range scanned {
		if target.sourceLocated && target.sourceEntry == latest {
			latestScanned = append(latestScanned, target)
		}
	}
	picker.LatestTargets = reconcileCodexArtifactPickerTargets(latestScanned, picker.LatestTargets, complete, picker.ProjectPath)
}

func renderCodexArtifactPickerScopes(picker *codexArtifactPickerState, scanning bool, width int) string {
	latest := fmt.Sprintf("Latest reply %d", len(picker.LatestTargets))
	all := fmt.Sprintf("All transcript %d", len(picker.Targets))
	if scanning {
		all += "+"
	}
	return fitFooterWidth(renderCodexArtifactPickerChoice(latest, picker.ShowLatest)+"  "+
		renderCodexArtifactPickerChoice(all, !picker.ShowLatest)+"  "+detailMutedStyle.Render("Tab switch"), width)
}

func renderCodexArtifactPickerTypes(picker *codexArtifactPickerState, width int) string {
	var counts [codexArtifactPickerTypeCount]int
	for _, target := range picker.activeTargets() {
		if !codexArtifactTargetMatchesFilter(target, picker.Filter, picker.ProjectPath) {
			continue
		}
		for kind := codexArtifactPickerAll; kind < codexArtifactPickerTypeCount; kind++ {
			if kind.matches(target) {
				counts[kind]++
			}
		}
	}
	var choices []string
	for kind := codexArtifactPickerAll; kind < codexArtifactPickerTypeCount; kind++ {
		choices = append(choices, renderCodexArtifactPickerChoice(fmt.Sprintf("%s %d", kind.label(), counts[kind]), picker.TypeFilter == kind))
	}
	return fitFooterWidth(strings.Join(choices, "  ")+"  "+detailMutedStyle.Render("←→ type"), width)
}

func renderCodexArtifactPickerChoice(label string, selected bool) string {
	if selected {
		return codexArtifactSelectedRowStyle().Render("[" + label + "]")
	}
	return detailMutedStyle.Render(label)
}
