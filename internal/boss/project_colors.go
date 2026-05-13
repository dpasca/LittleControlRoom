package boss

import (
	"hash/fnv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var bossProjectIdentityPalette = []lipgloss.Color{
	lipgloss.Color("117"),
	lipgloss.Color("114"),
	lipgloss.Color("179"),
	lipgloss.Color("208"),
	lipgloss.Color("141"),
	lipgloss.Color("217"),
	lipgloss.Color("80"),
	lipgloss.Color("186"),
	lipgloss.Color("109"),
	lipgloss.Color("215"),
	lipgloss.Color("156"),
	lipgloss.Color("183"),
}

func bossProjectIdentityStyle(identity string, base lipgloss.Style) lipgloss.Style {
	return base.Foreground(bossProjectIdentityColor(identity))
}

func bossProjectIdentityColor(identity string) lipgloss.Color {
	if len(bossProjectIdentityPalette) == 0 {
		return bossPanelText
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return bossPanelText
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(identity))
	return bossProjectIdentityPalette[int(hash.Sum32())%len(bossProjectIdentityPalette)]
}

func bossProjectBriefIdentity(project ProjectBrief) string {
	return firstNonEmpty(strings.TrimSpace(project.Path), strings.TrimSpace(project.Name))
}
