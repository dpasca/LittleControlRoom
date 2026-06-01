package uistyle

import "github.com/charmbracelet/lipgloss"

var (
	SidebarTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("81")).
				Bold(true)
	SidebarSectionHeaderStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("229")).
					Background(lipgloss.Color("235")).
					Bold(true)
)
