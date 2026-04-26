package tui

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"lcroom/internal/config"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type officeMockupVariant struct {
	name   string
	title  string
	width  int
	height int
	mode   string
	phase  int
}

type MockupRasterAsset struct {
	Name  string
	Title string
	Image image.Image
}

var (
	officeCabinBG        = lipgloss.Color("#09110d")
	officeCabinPanel     = lipgloss.Color("#14251d")
	officeCabinPanelDeep = lipgloss.Color("#0f1b16")
	officeCabinWood      = lipgloss.Color("#3a2a1e")
	officeCabinWoodSoft  = lipgloss.Color("#5b412d")
	officeCabinAmber     = lipgloss.Color("#f0c15b")
	officeCabinCream     = lipgloss.Color("#f4ead1")
	officeCabinMuted     = lipgloss.Color("#9aab92")
	officeCabinGreen     = lipgloss.Color("#86b36d")
	officeCabinReview    = lipgloss.Color("#d8b56a")
	officeCabinWatch     = lipgloss.Color("#89a7d8")
)

// GenerateMockups returns static, rendering-only design studies for the future
// high-level assistant view. It intentionally avoids service state so mockups
// can be regenerated without launching the live TUI.
func GenerateMockups(cfg config.ScreenshotConfig) ScreenshotReport {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	}()

	variants := []officeMockupVariant{
		{name: "boss-cabin-wide", title: "Boss Cabin - Wide", width: 112, height: 31, mode: "wide", phase: 0},
		{name: "boss-cabin-medium", title: "Boss Cabin - Medium", width: 86, height: 28, mode: "medium", phase: 1},
		{name: "boss-cabin-compact", title: "Boss Cabin - Compact", width: 60, height: 24, mode: "compact", phase: 2},
		{name: "boss-cabin-chat", title: "Boss Cabin - Chat", width: 112, height: 31, mode: "chat", phase: 3},
		{name: "boss-cabin-adventure", title: "Boss Cabin - Adventure", width: 112, height: 31, mode: "adventure", phase: 4},
		{name: "boss-cabin-adventure-narrow", title: "Boss Cabin - Adventure Narrow", width: 78, height: 28, mode: "adventure", phase: 5},
		{name: "boss-cabin-terminal", title: "Boss Cabin - Terminal Strict", width: 112, height: 31, mode: "terminal", phase: 6},
		{name: "boss-cabin-game", title: "Boss Cabin - Game Pass", width: 112, height: 31, mode: "game", phase: 7},
		{name: "boss-cabin-game-corner", title: "Boss Cabin - Game Corner", width: 112, height: 31, mode: "game-corner", phase: 8},
		{name: "boss-cabin-hires-corner", title: "Boss Cabin - Hi-Res Corner", width: 112, height: 31, mode: "hires-corner", phase: 9},
		{name: "boss-cabin-hires-8x-corner", title: "Boss Cabin - Hi-Res 8x Corner", width: 112, height: 31, mode: "hires-8x-corner", phase: 10},
		{name: "tiny-office-corner", title: "Tiny Office - Chat Corner", width: 112, height: 31, mode: "tiny-office-corner", phase: 11},
		{name: "tiny-office-room", title: "Tiny Office - Room View", width: 86, height: 28, mode: "tiny-office-room", phase: 12},
		{name: "tiny-office-alert", title: "Tiny Office - Attention State", width: 112, height: 31, mode: "tiny-office-alert", phase: 13},
	}

	assets := make([]ScreenshotAsset, 0, len(variants))
	for _, variant := range variants {
		localCfg := cfg
		localCfg.TerminalWidth = variant.width
		localCfg.TerminalHeight = variant.height
		rendered := renderOfficeCabinMockup(variant)
		assets = append(assets, screenshotAsset(variant.name, variant.title, rendered, localCfg))
	}
	return ScreenshotReport{Assets: assets}
}

func renderOfficeCabinMockup(variant officeMockupVariant) string {
	width := max(40, variant.width)
	height := max(12, variant.height)
	if variant.mode == "compact" {
		return officeFitContent(renderOfficeCabinCompact(width, height, variant.phase), width, height)
	}
	if variant.mode == "chat" {
		return officeFitContent(renderOfficeCabinChat(width, height, variant.phase), width, height)
	}
	if variant.mode == "adventure" {
		return officeFitContent(renderOfficeCabinAdventure(width, height, variant.phase), width, height)
	}
	if variant.mode == "terminal" {
		return officeFitContent(renderOfficeCabinTerminal(width, height, variant.phase), width, height)
	}
	if variant.mode == "game" {
		return officeFitContent(renderOfficeCabinGame(width, height, variant.phase), width, height)
	}
	if variant.mode == "game-corner" {
		return officeFitContent(renderOfficeCabinGameCorner(width, height, variant.phase), width, height)
	}
	if variant.mode == "hires-corner" {
		return officeFitContent(renderOfficeCabinHiResCorner(width, height, variant.phase), width, height)
	}
	if variant.mode == "hires-8x-corner" {
		return officeFitContent(renderOfficeCabinHiRes8xCorner(width, height, variant.phase), width, height)
	}
	if variant.mode == "tiny-office-corner" {
		return officeFitContent(renderOfficeTinyCorner(width, height, variant.phase), width, height)
	}
	if variant.mode == "tiny-office-room" {
		return officeFitContent(renderOfficeTinyRoom(width, height, variant.phase), width, height)
	}
	if variant.mode == "tiny-office-alert" {
		return officeFitContent(renderOfficeTinyAlert(width, height, variant.phase), width, height)
	}
	return officeFitContent(renderOfficeCabinDashboard(width, height, variant.phase), width, height)
}

func GenerateMockupRasterAssets(config.ScreenshotConfig) []MockupRasterAsset {
	_, sideW, _, sceneH, _, _ := officeGameCornerLayout(112, 31)
	source, _ := officeGameCornerSourceCanvas(sideW, sceneH, 10, 8)
	return []MockupRasterAsset{
		{
			Name:  "boss-cabin-hires-8x-source",
			Title: "Boss Cabin - Hi-Res 8x Source",
			Image: source.image(),
		},
	}
}

func renderOfficeCabinDashboard(width, height, phase int) string {
	lines := []string{
		officeHeaderLine(width, "Little Control Room", "Cozy Ops Cabin"),
		officeWindow(width, max(5, min(8, height/3)), phase),
	}
	lines = append(lines, officeAssistantBrief(width, phase)...)
	lines = append(lines, officeDashboardZones(width)...)
	lines = append(lines, officeFooterLine(width, "[/] boss chat   [a] safe chores   [enter] inspect   [tab] classic TUI"))
	return strings.Join(lines, "\n")
}

func renderOfficeCabinChat(width, height, phase int) string {
	lines := []string{
		officeHeaderLine(width, "Little Control Room", "Cabin Chat"),
		officeWindow(width, 6, phase),
	}
	chatW := max(34, (width*3)/5)
	sideW := max(24, width-chatW-2)
	chat := officeCard(chatW, "Conversation With Assistant", []string{
		"You: what do we do about LittleControlRoom?",
		"",
		"Assistant: I would review the office-mode mockup first.",
		"The work is low-risk, but it changes the app's",
		"personality, so I want your taste before code lands.",
		"",
		"Suggested path:",
		"1. Pick the cozy-cabin baseline.",
		"2. Keep classic TUI one key away.",
		"3. Make skinning semantic later.",
	}, officeCabinPanel)
	side := officeCard(sideW, "Current Room", []string{
		"Review Table",
		"  2 decisions",
		"",
		"Workbench",
		"  1 active build",
		"",
		"Quiet Shelf",
		"  8 parked projects",
	}, officeCabinPanelDeep)
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, "  ", side))...)
	lines = append(lines, officeFooterLine(width, "[enter] act on suggestion   [s] snooze   [d] details   [esc] room view"))
	return strings.Join(lines, "\n")
}

func renderOfficeTinyCorner(width, height, phase int) string {
	if width < 78 {
		return renderOfficeTinyRoom(width, height, phase)
	}
	bodyH := max(12, height-4)
	sideW := min(36, max(30, width/3))
	chatW := max(36, width-sideW-1)
	sceneH := min(11, max(9, bodyH/2))
	listH := min(8, max(6, bodyH/3))
	trayH := max(4, bodyH-sceneH-listH)

	chat := officeTinyChatPanel(chatW, bodyH, false)
	side := strings.Join([]string{
		officeTinyScenePanel(sideW, sceneH, false),
		officeTinyObjectPanel(sideW, listH, false),
		officeTinyTrayPanel(sideW, trayH, false),
	}, "\n")

	lines := []string{officeTinyHeader(width, "Tiny Control Room", "Assistant calm  /  Tab: classic")}
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, lipgloss.NewStyle().Width(1).Background(officeCabinBG).Render(""), side))...)
	lines = append(lines, splitLines(officeTinyVerbStrip(width))...)
	lines = append(lines, officeTinyStatus(width, false))
	return strings.Join(lines, "\n")
}

func renderOfficeTinyAlert(width, height, phase int) string {
	if width < 78 {
		return renderOfficeTinyRoom(width, height, phase)
	}
	bodyH := max(12, height-4)
	sideW := min(36, max(30, width/3))
	chatW := max(36, width-sideW-1)
	sceneH := min(11, max(9, bodyH/2))
	listH := min(8, max(6, bodyH/3))
	trayH := max(4, bodyH-sceneH-listH)

	chat := officeTinyChatPanel(chatW, bodyH, true)
	side := strings.Join([]string{
		officeTinyScenePanel(sideW, sceneH, true),
		officeTinyObjectPanel(sideW, listH, true),
		officeTinyTrayPanel(sideW, trayH, true),
	}, "\n")

	lines := []string{officeTinyHeader(width, "Tiny Control Room", "Assistant focused  /  1 lamp blinking")}
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, lipgloss.NewStyle().Width(1).Background(officeCabinBG).Render(""), side))...)
	lines = append(lines, splitLines(officeTinyVerbStrip(width))...)
	lines = append(lines, officeTinyStatus(width, true))
	return strings.Join(lines, "\n")
}

func renderOfficeTinyRoom(width, height, phase int) string {
	sceneH := min(11, max(8, height/2))
	gridH := 3
	chatH := max(6, height-sceneH-gridH-4)
	lines := []string{
		officeTinyHeader(width, "Tiny Control Room", "room view"),
		officeTinyScenePanel(width, sceneH, phase%2 == 1),
	}
	lines = append(lines, splitLines(officeTinyObjectGrid(width, gridH, phase%2 == 1))...)
	lines = append(lines, splitLines(officeTinyChatPanel(width, chatH, phase%2 == 1))...)
	lines = append(lines, splitLines(officeTinyVerbStrip(width))...)
	lines = append(lines, officeTinyStatus(width, phase%2 == 1))
	return strings.Join(lines, "\n")
}

func officeTinyHeader(width int, title, right string) string {
	left := " Little Control Room "
	center := " " + title + " "
	right = " " + right + " "
	space := max(1, width-len(left)-len(center)-len(right))
	text := left + strings.Repeat(" ", space/2) + center + strings.Repeat(" ", space-space/2) + right
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinCream).
		Background(officeCabinWood).
		Bold(true).
		Render(truncateText(text, width))
}

func officeTinyChatPanel(width, height int, alert bool) string {
	body := []string{
		"You: what needs me?",
		"",
		"Assistant: I made the room smaller on purpose.",
		"The objects are not decoration; they are project state.",
		"",
		"Desk    -> decisions for you",
		"Bench   -> active agents",
		"Lamp    -> blocked or risky",
		"Shelf   -> quiet / snoozed",
		"Notebook-> memory and daily log",
		"",
		"Suggested next move:",
		"  review the desk, leave the bench running.",
		"",
		"> REVIEW DESK",
		"> ASK MINA FOR OPTIONS",
		"> OPEN CLASSIC",
	}
	if alert {
		body = []string{
			"You: what needs me?",
			"",
			"Assistant: The desk has two decisions, and the lamp is blinking.",
			"The bench can keep working. I would not interrupt it.",
			"",
			"I can summarize the blocked item and keep the quiet shelf parked.",
			"Nothing else needs a boss decision yet.",
			"",
			"Suggested next move:",
			"  check the lamp, then review the desk.",
			"",
			"> CHECK LAMP",
			"> REVIEW DESK",
			"> SNOOZE QUIET SHELF",
		}
	}
	return officeGameSoftPanel(width, height, "Assistant, chief of staff", body, officeCabinPanel, officeCabinAmber)
}

func officeTinyScenePanel(width, height int, alert bool) string {
	body := officeTinySceneLines(max(10, width-2), alert)
	return officeTinyBox(width, height, "Assistant office", body, officeCabinPanelDeep, officeCabinAmber)
}

func officeTinySceneLines(width int, alert bool) []string {
	width = max(22, width)
	lamp := "[L] lamp calm"
	if alert {
		lamp = "[L] LAMP !"
	}
	lines := []string{
		"        shelf [Q] [Q]",
		"        quiet / parked",
		"",
		"   " + lamp + "      notebook",
		"                 [M] logs",
		"",
		"   desk [D]      bench [W]",
		"   papers:2      agents:3",
		"",
		"          [o_o] Assistant",
	}
	fitted := make([]string, 0, len(lines))
	for _, line := range lines {
		fitted = append(fitted, truncateText(line, width))
	}
	return fitted
}

func officeTinyObjectPanel(width, height int, alert bool) string {
	body := []string{
		"[D] Desk       2 decisions",
		"[W] Bench      3 agents active",
		"[L] Lamp       calm",
		"[Q] Shelf      8 quiet",
		"[M] Notebook   daily memory",
	}
	if alert {
		body[2] = "[L] Lamp       1 blocked"
	}
	return officeGameSoftPanel(width, height, "Office objects", body, officeCabinPanel, officeCabinGreen)
}

func officeTinyTrayPanel(width, height int, alert bool) string {
	body := []string{
		"Next:",
		"  Review desk papers",
		"",
		"Autopilot:",
		"  Let bench run",
	}
	if alert {
		body = []string{
			"Next:",
			"  Check blinking lamp",
			"  Review desk papers",
			"",
			"Autopilot:",
			"  Keep shelf snoozed",
		}
	}
	return officeGameSoftPanel(width, height, "Assistant tray", body, officeCabinPanel, officeCabinReview)
}

func officeTinyObjectGrid(width, height int, alert bool) string {
	gap := " "
	cardW := max(14, (width-lipgloss.Width(gap)*3)/4)
	cards := []string{
		officeTinySmallCard(cardW, "DESK", "2 decisions", officeCabinReview),
		officeTinySmallCard(cardW, "BENCH", "3 agents", officeCabinGreen),
		officeTinySmallCard(cardW, "LAMP", "calm", officeCabinWatch),
		officeTinySmallCard(cardW, "SHELF", "8 quiet", officeCabinMuted),
	}
	if alert {
		cards[2] = officeTinySmallCard(cardW, "LAMP", "1 blocked", lipgloss.Color("#e06b5f"))
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, cards[0], gap, cards[1], gap, cards[2], gap, cards[3])
	lines := splitLines(row)
	for len(lines) < height {
		lines = append(lines, lipgloss.NewStyle().Width(width).Background(officeCabinBG).Render(""))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func officeTinySmallCard(width int, title, value string, accent lipgloss.Color) string {
	titleStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinBG).Background(accent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinCream).Background(officeCabinPanel)
	return strings.Join([]string{
		titleStyle.Render(" " + truncateText(title, max(1, width-2))),
		bodyStyle.Render(" " + truncateText(value, max(1, width-2))),
		bodyStyle.Render(" " + truncateText("inspect >", max(1, width-2))),
	}, "\n")
}

func officeTinyBox(width, height int, title string, body []string, bg, accent lipgloss.Color) string {
	width = max(8, width)
	height = max(2, height)
	titleStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinBG).Background(accent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinCream).Background(bg)
	mutedStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinMuted).Background(bg)

	lines := []string{titleStyle.Render(" " + truncateText(title, max(1, width-2)))}
	for _, line := range body {
		if len(lines) >= height {
			break
		}
		style := bodyStyle
		if strings.TrimSpace(line) == "" || strings.Contains(line, "quiet") || strings.Contains(line, "calm") {
			style = mutedStyle
		}
		lines = append(lines, style.Render(" "+truncateText(line, max(1, width-2))))
	}
	for len(lines) < height {
		lines = append(lines, bodyStyle.Render(""))
	}
	return strings.Join(lines, "\n")
}

func officeTinyVerbStrip(width int) string {
	verbs := " [L] LOOK   [T] TALK TO MINA   [D] REVIEW DESK   [W] CHECK BENCH   [S] SNOOZE   [TAB] CLASSIC "
	help := " semantic room objects: desk / bench / lamp / shelf / notebook "
	return strings.Join([]string{
		lipgloss.NewStyle().Width(width).Foreground(officeCabinAmber).Background(officeCabinWood).Bold(true).Render(truncateText(verbs, width)),
		lipgloss.NewStyle().Width(width).Foreground(officeCabinMuted).Background(officeCabinWood).Render(truncateText(help, width)),
	}, "\n")
}

func officeTinyStatus(width int, alert bool) string {
	text := " Trailblazer   Desk 2   Bench 3 active   Lamp calm   Shelf 8 quiet   Classic: tab "
	if alert {
		text = " Trailblazer   Desk 2   Bench 3 active   Lamp 1 blocked   Shelf 8 quiet   Classic: tab "
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinGreen).
		Background(officeCabinPanelDeep).
		Render(truncateText(text, width))
}

func renderOfficeCabinCompact(width, height, phase int) string {
	lines := []string{
		officeHeaderLine(width, "Little Control Room", "Cabin"),
		officeWindow(width, 5, phase),
	}
	lines = append(lines, officeAssistantBrief(width, phase)...)
	lines = append(lines,
		officeCompactRow(width, "Review Table", "2 items need you", officeCabinReview),
		officeCompactRow(width, "Workbench", "1 agent active, tests running", officeCabinGreen),
		officeCompactRow(width, "Waiting Chair", "1 blocked on a reply", officeCabinWatch),
		officeCompactRow(width, "Quiet Shelf", "8 snoozed or calm", officeCabinMuted),
	)
	lines = append(lines, officeFooterLine(width, "[/] chat  [enter] inspect  [c] classic"))
	return strings.Join(lines, "\n")
}

func renderOfficeCabinAdventure(width, height, phase int) string {
	if width < 90 {
		return renderOfficeCabinAdventureStacked(width, height, phase)
	}
	bodyH := max(16, height-3)
	chatW := max(48, (width*58)/100)
	sideW := max(28, width-chatW-2)
	sceneH := min(13, max(9, bodyH/2))
	spotsH := max(8, bodyH-sceneH-1)

	chat := officePanel(chatW, bodyH, "Boss Chat", []string{
		"You: what do we do first?",
		"",
		"Assistant: I would look at LittleControlRoom.",
		"It is not on fire, but it changes the soul of the app.",
		"The cabin view should feel calm before we wire it to real",
		"project-management inference.",
		"",
		"I can prepare the safe chores, keep the quiet shelf tidy,",
		"and ask before anything risky like commit, push, or agent",
		"instructions.",
		"",
		"> REVIEW papers on the desk",
		"> LOOK AT the red lantern",
		"> OPEN CLASSIC control room",
	}, officeCabinPanel)
	scene := officeScenePanel(sideW, sceneH, "Cabin Control Room", officeAdventureScene(sideW, sceneH-1, phase), officeCabinPanelDeep)
	spots := officePanel(sideW, spotsH, "Important Spots", []string{
		"Desk",
		"  2 decisions waiting for the boss",
		"",
		"Workbench",
		"  1 active agent, build lamp green",
		"",
		"Red lantern",
		"  1 blocked item, not urgent",
		"",
		"Quiet shelf",
		"  8 parked projects",
	}, officeCabinPanel)
	side := strings.Join([]string{scene, lipgloss.NewStyle().Width(sideW).Background(officeCabinBG).Render(""), spots}, "\n")
	lines := []string{officeHeaderLine(width, "Little Control Room", "Adventure Boss Mode")}
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, "  ", side))...)
	lines = append(lines, officeFooterLine(width, "LOOK AT   TALK TO   REVIEW   SNOOZE   DELEGATE SAFE CHORES   OPEN CLASSIC"))
	lines = append(lines, officeFooterLine(width, "Inventory: [2 decisions] [3 safe chores] [1 blocked] [8 quiet]"))
	return strings.Join(lines, "\n")
}

func renderOfficeCabinAdventureStacked(width, height, phase int) string {
	sceneH := min(8, max(5, height/3))
	chatH := min(10, max(7, height-sceneH-8))
	lines := []string{
		officeHeaderLine(width, "Little Control Room", "Adventure Boss Mode"),
		officeScenePanel(width, sceneH, "Cabin Control Room", officeAdventureScene(width, sceneH-1, phase), officeCabinPanelDeep),
		officePanel(width, chatH, "Boss Chat", []string{
			"You: what do we do first?",
			"",
			"Assistant: Review the papers on the desk.",
			"The cabin can stay calm while I keep the room sorted.",
			"",
			"> REVIEW desk papers",
		}, officeCabinPanel),
		officeCompactRow(width, "Desk", "2 decisions", officeCabinReview),
		officeCompactRow(width, "Workbench", "1 active agent", officeCabinGreen),
		officeCompactRow(width, "Lantern", "1 blocked, not urgent", officeCabinWatch),
		officeFooterLine(width, "LOOK  TALK  REVIEW  SNOOZE  CLASSIC"),
	}
	return strings.Join(lines, "\n")
}

func renderOfficeCabinTerminal(width, height, phase int) string {
	if width < 92 {
		return renderOfficeCabinTerminalStacked(width, height, phase)
	}

	actionH := 4
	statusH := 1
	bodyH := max(16, height-actionH-statusH-1)
	chatW := min(66, max(52, (width*56)/100))
	sideW := max(32, width-chatW-2)
	if chatW+sideW+2 > width {
		sideW = max(28, width-chatW-2)
	}
	sceneH := min(13, max(10, bodyH/2))
	spotsH := max(9, bodyH-sceneH)

	chat := officeTermBox(chatW, bodyH, "Assistant - your teammate [online]", []string{
		"  [o_o]  Assistant",
		"  " + strings.Repeat("-", max(8, chatW-12)),
		"",
		"Assistant: Hey. Welcome back to Little Control Room.",
		"      I see four room objects worth attention.",
		"",
		"You : Show me what needs attention.",
		"",
		"Assistant: Sure thing. Here is the quick overview.",
		"      [R] Review Table : 2 decisions need you",
		"      [W] Workbench    : 3 agents are coding",
		"      [L] Lantern      : 1 item is blocked",
		"      [Q] Quiet Shelf  : 2 items are snoozed",
		"",
		"You : Let's check the review table.",
		"",
		"Assistant: Great. I will bring up the open decisions.",
		"      Take your time. I am here when you are ready.",
		"",
		"+ " + truncateText("You: _", max(1, chatW-8)),
	}, officeCabinPanel, officeCabinAmber)

	scene := officeTermBox(sideW, sceneH, "Cabin control room", officeTerminalScene(sideW-2, sceneH-2, phase), officeCabinPanelDeep, officeCabinWoodSoft)
	spots := officeTermBox(sideW, spotsH, "Important spots", officeTerminalSpotLines(sideW-2), officeCabinPanel, officeCabinAmber)
	side := strings.Join([]string{scene, spots}, "\n")

	lines := []string{officeTerminalHeader(width)}
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, "  ", side))...)
	lines = append(lines, officeVerbBar(width))
	lines = append(lines, officeStatusBar(width))
	return strings.Join(lines, "\n")
}

func renderOfficeCabinGame(width, height, phase int) string {
	if width < 82 {
		return renderOfficeCabinGameStacked(width, height, phase)
	}

	dialogueH := 7
	verbH := 2
	statusH := 1
	stageH := max(10, height-dialogueH-verbH-statusH-1)

	lines := []string{officeGameHeader(width)}
	lines = append(lines, splitLines(officeGameStage(width, stageH, phase))...)
	lines = append(lines, splitLines(officeGameDialogueRow(width, dialogueH))...)
	lines = append(lines, splitLines(officeGameVerbStrip(width))...)
	lines = append(lines, officeGameStatus(width))
	return strings.Join(lines, "\n")
}

func renderOfficeCabinGameCorner(width, height, phase int) string {
	if width < 82 {
		return renderOfficeCabinGameStacked(width, height, phase)
	}

	bodyH, sideW, chatW, sceneH, spotsH, agendaH := officeGameCornerLayout(width, height)

	chat := officeGameLongChatPanel(chatW, bodyH)
	side := strings.Join([]string{
		officeGameCornerScene(sideW, sceneH, phase),
		officeGameHotspotsPanel(sideW, spotsH),
		officeGameAgendaPanel(sideW, agendaH),
	}, "\n")

	lines := []string{officeGameHeader(width)}
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, lipgloss.NewStyle().Width(1).Background(officeCabinBG).Render(""), side))...)
	lines = append(lines, splitLines(officeGameVerbStrip(width))...)
	lines = append(lines, officeGameStatus(width))
	return strings.Join(lines, "\n")
}

func renderOfficeCabinHiResCorner(width, height, phase int) string {
	if width < 82 {
		return renderOfficeCabinGameStacked(width, height, phase)
	}

	bodyH, sideW, chatW, sceneH, spotsH, agendaH := officeGameCornerLayout(width, height)

	chat := officeGameLongChatPanel(chatW, bodyH)
	side := strings.Join([]string{
		officeGameCornerSceneHiRes(sideW, sceneH, phase, 4, officeDownsampleAverage),
		officeGameHotspotsPanel(sideW, spotsH),
		officeGameAgendaPanel(sideW, agendaH),
	}, "\n")

	lines := []string{officeGameHeader(width)}
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, lipgloss.NewStyle().Width(1).Background(officeCabinBG).Render(""), side))...)
	lines = append(lines, splitLines(officeGameVerbStrip(width))...)
	lines = append(lines, officeGameStatus(width))
	return strings.Join(lines, "\n")
}

func renderOfficeCabinHiRes8xCorner(width, height, phase int) string {
	if width < 82 {
		return renderOfficeCabinGameStacked(width, height, phase)
	}

	bodyH, sideW, chatW, sceneH, spotsH, agendaH := officeGameCornerLayout(width, height)

	chat := officeGameLongChatPanel(chatW, bodyH)
	side := strings.Join([]string{
		officeGameCornerSceneHiRes(sideW, sceneH, phase, 8, officeDownsampleCrisp),
		officeGameHotspotsPanel(sideW, spotsH),
		officeGameAgendaPanel(sideW, agendaH),
	}, "\n")

	lines := []string{officeGameHeader(width)}
	lines = append(lines, splitLines(lipgloss.JoinHorizontal(lipgloss.Top, chat, lipgloss.NewStyle().Width(1).Background(officeCabinBG).Render(""), side))...)
	lines = append(lines, splitLines(officeGameVerbStrip(width))...)
	lines = append(lines, officeGameStatus(width))
	return strings.Join(lines, "\n")
}

func officeGameCornerLayout(width, height int) (bodyH, sideW, chatW, sceneH, spotsH, agendaH int) {
	verbH := 2
	statusH := 1
	bodyH = max(14, height-verbH-statusH-1)
	sideW = min(46, max(36, width/3))
	chatW = max(34, width-sideW-1)
	sceneH = min(11, max(8, bodyH/2))
	spotsH = min(8, max(6, bodyH/3))
	agendaH = max(4, bodyH-sceneH-spotsH)
	return bodyH, sideW, chatW, sceneH, spotsH, agendaH
}

func renderOfficeCabinGameStacked(width, height, phase int) string {
	dialogueH := 7
	verbH := 2
	statusH := 1
	stageH := max(8, height-dialogueH-verbH-statusH-4)

	lines := []string{officeGameHeader(width)}
	lines = append(lines, splitLines(officeGameStage(width, stageH, phase))...)
	lines = append(lines, splitLines(officeGameDialoguePanel(width, dialogueH))...)
	lines = append(lines, officeCompactRow(width, "[R] Desk", "2 decisions need your taste", officeCabinReview))
	lines = append(lines, officeCompactRow(width, "[W] Bench", "3 coding agents active", officeCabinGreen))
	lines = append(lines, officeCompactRow(width, "[L] Lantern", "1 blocked item", officeCabinWatch))
	lines = append(lines, splitLines(officeGameVerbStrip(width))...)
	lines = append(lines, officeGameStatus(width))
	return strings.Join(lines, "\n")
}

func officeGameHeader(width int) string {
	left := " Little Control Room "
	center := " Boss Cabin "
	right := " Assistant calm  /  Tab: classic "
	space := max(1, width-len(left)-len(center)-len(right))
	text := left + strings.Repeat(" ", space/2) + center + strings.Repeat(" ", space-space/2) + right
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinCream).
		Background(officeCabinWood).
		Bold(true).
		Render(truncateText(text, width))
}

func officeGameStage(width, height, phase int) string {
	width = max(30, width)
	height = max(6, height)
	canvasH := max(2, height*2)
	canvas := newRuntimeFlairCanvas(width, canvasH)

	wallTop := runtimeFlairRGB(7, 17, 17)
	wall := runtimeFlairRGB(13, 31, 25)
	wallGlow := runtimeFlairRGB(20, 45, 35)
	beam := runtimeFlairRGB(58, 41, 28)
	beamSoft := runtimeFlairRGB(82, 58, 37)
	floor := runtimeFlairRGB(64, 42, 28)
	floorEdge := runtimeFlairRGB(119, 84, 50)
	windowFrame := runtimeFlairRGB(97, 65, 39)
	sky := runtimeFlairRGB(29, 63, 67)
	skyDeep := runtimeFlairRGB(16, 42, 49)
	mountain := runtimeFlairRGB(93, 135, 103)
	mountainDark := runtimeFlairRGB(62, 95, 73)
	river := runtimeFlairRGB(70, 163, 151)
	tree := runtimeFlairRGB(41, 92, 57)
	treeDeep := runtimeFlairRGB(26, 68, 47)
	lamp := runtimeFlairRGB(242, 184, 79)
	lampGlow := runtimeFlairRGB(141, 87, 47)
	paper := runtimeFlairRGB(235, 222, 184)
	shelf := runtimeFlairRGB(108, 76, 49)
	rug := runtimeFlairRGB(75, 95, 77)

	canvas.fillRect(0, 0, width, canvasH, wallTop)
	canvas.fillRect(0, max(1, canvasH/6), width, max(1, (canvasH*2)/3), wall)
	canvas.fillRect(0, max(2, canvasH/3), width, max(1, canvasH/4), wallGlow)
	for x := 0; x < width; x += max(12, width/7) {
		canvas.fillRect(x, 0, 2, canvasH, runtimeFlairRGB(8, 22, 18))
	}

	floorTop := max(4, canvasH-8)
	canvas.fillRect(0, floorTop, width, canvasH-floorTop, floor)
	canvas.fillRect(0, floorTop, width, 1, floorEdge)
	for y := floorTop + 2; y < canvasH; y += 4 {
		canvas.fillRect(0, y, width, 1, runtimeFlairRGB(48, 32, 24))
	}

	windowW := min(max(30, width/3), max(18, width-12))
	windowH := min(max(12, canvasH/3), max(8, floorTop-4))
	windowX := 4
	windowY := 3
	canvas.drawOutlinedRect(windowX, windowY, windowW, windowH, windowFrame, skyDeep)
	canvas.fillRect(windowX+2, windowY+2, max(1, windowW-4), max(1, windowH-4), sky)
	for i := 0; i < windowW-5; i += 7 {
		x := windowX + 3 + i
		base := windowY + windowH - 3
		canvas.fillRect(x, base-3, 2, 3, mountainDark)
		canvas.fillRect(x+2, base-5, 2, 5, mountain)
		canvas.fillRect(x+4, base-2, 2, 2, mountainDark)
	}
	for i := 0; i < windowW-5; i++ {
		if (i+phase)%4 != 0 {
			canvas.set(windowX+3+i, windowY+windowH-4, river)
		}
	}
	canvas.fillRect(windowX+windowW/2, windowY+1, 1, windowH-2, windowFrame)
	canvas.fillRect(windowX+1, windowY+windowH/2, windowW-2, 1, windowFrame)

	for i := 0; i < min(width, 28); i += 4 {
		x := width - 30 + i
		if x < 1 || x >= width-2 {
			continue
		}
		canvas.fillRect(x, max(4, floorTop-13+(i%3)), 3, 11-(i%3), treeDeep)
		canvas.fillRect(x+1, max(3, floorTop-15+(i%4)), 2, 3, tree)
	}

	shelfX := max(width-29, windowX+windowW+5)
	shelfY := max(5, floorTop-17)
	canvas.fillRect(shelfX, shelfY, min(21, width-shelfX-2), 2, shelf)
	canvas.fillRect(shelfX+2, shelfY-5, 3, 5, paper)
	canvas.fillRect(shelfX+7, shelfY-7, 4, 7, runtimeFlairSignalGoodColor)
	canvas.fillRect(shelfX+13, shelfY-4, 5, 4, runtimeFlairMonitorDimColor)
	canvas.fillRect(shelfX+2, shelfY+5, min(19, width-shelfX-4), 2, shelf)
	canvas.fillRect(shelfX+4, shelfY+1, 2, 4, runtimeFlairSignalWarmColor)
	canvas.fillRect(shelfX+9, shelfY+1, 2, 4, runtimeFlairSignalHotColor)

	lampX := min(width-11, max(windowX+windowW+10, (width*7)/10))
	lampY := max(5, floorTop-20)
	canvas.fillRect(lampX-3, lampY-2, 8, 5, lampGlow)
	canvas.fillRect(lampX, lampY, 3, 5, lamp)
	canvas.fillRect(lampX+1, lampY+5, 1, max(1, floorTop-lampY-5), beamSoft)
	canvas.fillRect(lampX, floorTop-2, 3, 2, runtimeFlairSignalHotColor)

	deskW := min(24, max(16, width/4))
	deskX := max(windowX+windowW+3, min(width-deskW-9, width/2+4))
	deskY := max(10, floorTop-8)
	canvas.fillRect(max(1, deskX-9), max(1, deskY+3), min(width-2, deskW+20), 4, rug)
	runtimeFlairDrawDesk(&canvas, deskX, deskY, deskW, phase%runtimeFlairCycleSize)
	canvas.fillRect(deskX+2, deskY-2, 5, 1, paper)
	canvas.fillRect(deskX+8, deskY-2, 4, 1, paper)

	operatorX := max(4, min(width-18, deskX-14))
	runtimeFlairDrawOperator(&canvas, runtimeFlairOperatorState{
		x:      operatorX,
		y:      max(3, floorTop-14),
		facing: 1,
		pose:   runtimeFlairOperatorInspect,
		blink:  phase%5 == 0,
	})

	canvas.fillRect(0, 0, width, 2, beam)
	canvas.fillRect(0, floorTop-1, width, 1, beamSoft)

	return canvas.render()
}

func officeGameCornerScene(width, height, phase int) string {
	width = max(30, width)
	height = max(6, height)
	canvasH := max(2, height*2)
	canvas := newRuntimeFlairCanvas(width, canvasH)

	wall := runtimeFlairRGB(12, 31, 25)
	wallDeep := runtimeFlairRGB(6, 17, 17)
	wallGlow := runtimeFlairRGB(22, 48, 37)
	wood := runtimeFlairRGB(78, 52, 34)
	woodLight := runtimeFlairRGB(128, 88, 51)
	sky := runtimeFlairRGB(34, 72, 76)
	river := runtimeFlairRGB(72, 164, 150)
	mountain := runtimeFlairRGB(94, 136, 104)
	floor := runtimeFlairRGB(66, 44, 29)
	rug := runtimeFlairRGB(76, 94, 78)
	lamp := runtimeFlairRGB(242, 184, 79)
	paper := runtimeFlairRGB(235, 222, 184)

	canvas.fillRect(0, 0, width, canvasH, wallDeep)
	canvas.fillRect(0, max(1, canvasH/5), width, max(1, canvasH-canvasH/5), wall)
	canvas.fillRect(0, max(3, canvasH/2), width, max(1, canvasH/4), wallGlow)
	for x := 0; x < width; x += max(10, width/5) {
		canvas.fillRect(x, 0, 2, canvasH, runtimeFlairRGB(7, 23, 19))
	}

	floorTop := max(4, canvasH-6)
	canvas.fillRect(0, floorTop, width, canvasH-floorTop, floor)
	canvas.fillRect(0, floorTop, width, 1, woodLight)

	windowW := min(width-8, max(18, width/2))
	windowH := min(max(8, canvasH/3), max(5, floorTop-3))
	canvas.drawOutlinedRect(3, 3, windowW, windowH, wood, sky)
	for i := 0; i < windowW-4; i += 6 {
		x := 5 + i
		base := 3 + windowH - 2
		canvas.fillRect(x, base-2, 2, 2, mountain)
		canvas.fillRect(x+2, base-4, 2, 4, mountain)
	}
	for i := 0; i < windowW-5; i++ {
		if (i+phase)%3 != 0 {
			canvas.set(5+i, 3+windowH-3, river)
		}
	}

	deskW := min(16, max(11, width/3))
	deskX := min(width-deskW-3, max(12, width/2))
	deskY := max(7, floorTop-6)
	canvas.fillRect(max(1, deskX-7), deskY+3, min(width-2, deskW+12), 3, rug)
	runtimeFlairDrawDesk(&canvas, deskX, deskY, deskW, phase%runtimeFlairCycleSize)
	canvas.fillRect(deskX+1, deskY-2, 4, 1, paper)

	operatorX := max(2, deskX-12)
	runtimeFlairDrawOperator(&canvas, runtimeFlairOperatorState{
		x:      operatorX,
		y:      max(2, floorTop-13),
		facing: 1,
		pose:   runtimeFlairOperatorInspect,
		blink:  phase%5 == 0,
	})

	shelfX := max(width-12, deskX+deskW+1)
	shelfY := max(4, deskY-6)
	canvas.fillRect(shelfX, shelfY, min(10, width-shelfX-1), 1, wood)
	canvas.fillRect(shelfX+1, shelfY-3, 2, 3, runtimeFlairSignalGoodColor)
	canvas.fillRect(shelfX+4, shelfY-2, 3, 2, runtimeFlairMonitorDimColor)
	canvas.fillRect(shelfX+1, shelfY+4, min(9, width-shelfX-2), 1, wood)
	canvas.fillRect(shelfX+2, shelfY+1, 2, 3, lamp)
	canvas.fillRect(shelfX+6, shelfY+1, 2, 3, runtimeFlairSignalHotColor)

	canvas.fillRect(0, 0, width, 2, wood)
	canvas.fillRect(0, floorTop-1, width, 1, woodLight)
	return canvas.render()
}

func officeGameCornerSceneHiRes(width, height, phase, scale int, mode officeDownsampleMode) string {
	canvas, subH := officeGameCornerSourceCanvas(width, height, phase, scale)
	return canvas.downsample(width, subH, scale, mode).render()
}

func officeGameCornerSourceCanvas(width, height, phase, scale int) (officeHiResCanvas, int) {
	width = max(30, width)
	height = max(6, height)
	scale = max(1, scale)
	subH := max(2, height*2)
	canvas := newOfficeHiResCanvas(width*scale, subH*scale, runtimeFlairRGB(6, 17, 17))

	wall := runtimeFlairRGB(12, 31, 25)
	wallGlow := runtimeFlairRGB(23, 51, 39)
	wallRib := runtimeFlairRGB(6, 23, 19)
	wood := runtimeFlairRGB(78, 52, 34)
	woodLight := runtimeFlairRGB(128, 88, 51)
	sky := runtimeFlairRGB(35, 76, 80)
	skyDeep := runtimeFlairRGB(20, 50, 58)
	river := runtimeFlairRGB(75, 172, 158)
	mountain := runtimeFlairRGB(94, 136, 104)
	mountainDark := runtimeFlairRGB(58, 91, 69)
	floor := runtimeFlairRGB(66, 44, 29)
	rug := runtimeFlairRGB(76, 94, 78)
	lamp := runtimeFlairRGB(242, 184, 79)
	lampGlow := runtimeFlairRGB(135, 91, 45)
	paper := runtimeFlairRGB(235, 222, 184)
	monitor := runtimeFlairRGB(79, 214, 203)
	monitorDim := runtimeFlairRGB(52, 156, 149)
	frame := runtimeFlairRGB(44, 69, 74)
	skin := runtimeFlairRGB(240, 198, 136)
	hair := runtimeFlairRGB(123, 75, 41)
	shirt := runtimeFlairRGB(75, 142, 194)
	shirtLight := runtimeFlairRGB(141, 203, 232)
	pants := runtimeFlairRGB(76, 54, 42)

	fillRect := func(x, y, w, h int, color runtimeFlairColor) {
		canvas.fillRect(x*scale, y*scale, w*scale, h*scale, color)
	}
	fillEllipse := func(cx, cy, rx, ry int, color runtimeFlairColor) {
		canvas.fillEllipse(cx*scale, cy*scale, max(1, rx*scale), max(1, ry*scale), color)
	}
	fillTriangle := func(x1, y1, x2, y2, x3, y3 int, color runtimeFlairColor) {
		canvas.fillTriangle(x1*scale, y1*scale, x2*scale, y2*scale, x3*scale, y3*scale, color)
	}

	fillRect(0, subH/5, width, subH, wall)
	fillRect(0, subH/2, width, max(1, subH/4), wallGlow)
	for x := 0; x < width; x += max(9, width/5) {
		fillRect(x, 0, 2, subH, wallRib)
	}

	floorTop := max(4, subH-6)
	fillRect(0, floorTop, width, subH-floorTop, floor)
	fillRect(0, floorTop, width, 1, woodLight)

	windowX := 3
	windowY := 3
	windowW := min(width-8, max(18, width/2))
	windowH := min(max(8, subH/3), max(5, floorTop-3))
	fillRect(windowX, windowY, windowW, windowH, wood)
	fillRect(windowX+1, windowY+1, windowW-2, windowH-2, skyDeep)
	fillRect(windowX+2, windowY+2, windowW-4, windowH-4, sky)
	for i := 0; i < windowW-4; i += 6 {
		x := windowX + 3 + i
		base := windowY + windowH - 2
		fillTriangle(x-2, base, x+2, base-6, x+7, base, mountainDark)
		fillTriangle(x+1, base, x+5, base-8, x+10, base, mountain)
	}
	for i := 0; i < windowW-5; i++ {
		if (i+phase)%3 != 0 {
			fillRect(windowX+2+i, windowY+windowH-3, 1, 1, river)
		}
	}
	fillRect(windowX+windowW/2, windowY+1, 1, windowH-2, wood)
	fillRect(windowX+1, windowY+windowH/2, windowW-2, 1, wood)

	deskW := min(16, max(11, width/3))
	deskX := min(width-deskW-3, max(12, width/2))
	deskY := max(7, floorTop-6)
	fillRect(max(1, deskX-7), deskY+3, min(width-2, deskW+12), 3, rug)

	monitorX := deskX + max(2, deskW/3)
	monitorY := deskY - 5
	fillRect(monitorX, monitorY, 9, 4, frame)
	fillRect(monitorX+1, monitorY+1, 7, 2, monitor)
	fillRect(monitorX+4, monitorY+4, 2, 1, frame)
	fillRect(deskX, deskY, deskW, 4, runtimeFlairDeskShadowColor)
	fillRect(deskX+1, deskY+1, max(1, deskW-2), 2, runtimeFlairDeskColor)
	fillRect(deskX+2, deskY-1, max(6, deskW-4), 1, runtimeFlairKeyboardColor)
	fillRect(deskX+1, deskY+1, 1, 1, runtimeFlairSignalGoodColor)
	fillRect(deskX+3, deskY+1, 1, 1, runtimeFlairSignalWarmColor)
	fillRect(deskX+deskW-2, deskY-1, 1, 3, paper)

	operatorX := max(3, deskX-11)
	operatorY := max(2, floorTop-13)
	fillEllipse(operatorX+5, operatorY+3, 5, 3, skin)
	fillRect(operatorX+1, operatorY+1, 8, 2, hair)
	fillRect(operatorX+2, operatorY+5, 6, 4, shirt)
	fillRect(operatorX+3, operatorY+6, 4, 1, shirtLight)
	fillRect(operatorX+1, operatorY+6, 2, 2, shirt)
	fillRect(operatorX+8, operatorY+6, 2, 2, shirt)
	fillRect(operatorX+2, operatorY+9, 2, 4, pants)
	fillRect(operatorX+6, operatorY+9, 2, 4, pants)
	fillRect(operatorX+4, operatorY+4, 1, 1, runtimeFlairOutlineColor)
	if phase%5 != 0 {
		fillRect(operatorX+7, operatorY+4, 1, 1, runtimeFlairOutlineColor)
	}

	shelfX := max(width-12, deskX+deskW+1)
	shelfY := max(4, deskY-6)
	fillRect(shelfX, shelfY, min(10, width-shelfX-1), 1, wood)
	fillRect(shelfX+1, shelfY-3, 2, 3, runtimeFlairSignalGoodColor)
	fillRect(shelfX+4, shelfY-2, 3, 2, monitorDim)
	fillRect(shelfX+1, shelfY+4, min(9, width-shelfX-2), 1, wood)
	fillEllipse(shelfX+2, shelfY+2, 4, 4, lampGlow)
	fillRect(shelfX+2, shelfY+1, 2, 3, lamp)
	fillRect(shelfX+6, shelfY+1, 2, 3, runtimeFlairSignalHotColor)

	fillRect(0, 0, width, 2, wood)
	fillRect(0, floorTop-1, width, 1, woodLight)

	return canvas, subH
}

type officeHiResCanvas struct {
	width  int
	height int
	pixels []runtimeFlairColor
}

type officeDownsampleMode int

const (
	officeDownsampleAverage officeDownsampleMode = iota
	officeDownsampleCrisp
)

func newOfficeHiResCanvas(width, height int, bg runtimeFlairColor) officeHiResCanvas {
	width = max(0, width)
	height = max(0, height)
	pixels := make([]runtimeFlairColor, width*height)
	if bg.valid {
		for i := range pixels {
			pixels[i] = bg
		}
	}
	return officeHiResCanvas{width: width, height: height, pixels: pixels}
}

func (c *officeHiResCanvas) set(x, y int, color runtimeFlairColor) {
	if !color.valid || x < 0 || y < 0 || x >= c.width || y >= c.height {
		return
	}
	c.pixels[y*c.width+x] = color
}

func (c *officeHiResCanvas) colorAt(x, y int) runtimeFlairColor {
	if x < 0 || y < 0 || x >= c.width || y >= c.height {
		return runtimeFlairColor{}
	}
	return c.pixels[y*c.width+x]
}

func (c officeHiResCanvas) image() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	for y := 0; y < c.height; y++ {
		for x := 0; x < c.width; x++ {
			src := c.colorAt(x, y)
			if !src.valid {
				img.Set(x, y, color.RGBA{A: 0xff})
				continue
			}
			img.Set(x, y, color.RGBA{R: src.r, G: src.g, B: src.b, A: 0xff})
		}
	}
	return img
}

func (c *officeHiResCanvas) fillRect(x, y, width, height int, color runtimeFlairColor) {
	if width <= 0 || height <= 0 || !color.valid {
		return
	}
	xStart := max(0, x)
	yStart := max(0, y)
	xEnd := min(c.width, x+width)
	yEnd := min(c.height, y+height)
	for py := yStart; py < yEnd; py++ {
		for px := xStart; px < xEnd; px++ {
			c.set(px, py, color)
		}
	}
}

func (c *officeHiResCanvas) fillEllipse(cx, cy, rx, ry int, color runtimeFlairColor) {
	if rx <= 0 || ry <= 0 || !color.valid {
		return
	}
	rx2 := rx * rx
	ry2 := ry * ry
	limit := rx2 * ry2
	for y := cy - ry; y <= cy+ry; y++ {
		for x := cx - rx; x <= cx+rx; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx*ry2+dy*dy*rx2 <= limit {
				c.set(x, y, color)
			}
		}
	}
}

func (c *officeHiResCanvas) fillTriangle(x1, y1, x2, y2, x3, y3 int, color runtimeFlairColor) {
	if !color.valid {
		return
	}
	minX := max(0, min(x1, min(x2, x3)))
	maxX := min(c.width-1, max(x1, max(x2, x3)))
	minY := max(0, min(y1, min(y2, y3)))
	maxY := min(c.height-1, max(y1, max(y2, y3)))
	edge := func(ax, ay, bx, by, px, py int) int {
		return (px-ax)*(by-ay) - (py-ay)*(bx-ax)
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			e1 := edge(x1, y1, x2, y2, x, y)
			e2 := edge(x2, y2, x3, y3, x, y)
			e3 := edge(x3, y3, x1, y1, x, y)
			hasNeg := e1 < 0 || e2 < 0 || e3 < 0
			hasPos := e1 > 0 || e2 > 0 || e3 > 0
			if !(hasNeg && hasPos) {
				c.set(x, y, color)
			}
		}
	}
}

func (c officeHiResCanvas) downsample(width, subpixelHeight, scale int, mode officeDownsampleMode) runtimeFlairCanvas {
	scale = max(1, scale)
	out := newRuntimeFlairCanvas(width, subpixelHeight)
	for y := 0; y < subpixelHeight; y++ {
		for x := 0; x < width; x++ {
			var r, g, b, count int
			counts := make(map[runtimeFlairColor]int, scale*scale)
			var dominant runtimeFlairColor
			dominantCount := 0
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					color := c.colorAt(x*scale+sx, y*scale+sy)
					if !color.valid {
						continue
					}
					r += int(color.r)
					g += int(color.g)
					b += int(color.b)
					count++
					counts[color]++
					if counts[color] > dominantCount {
						dominant = color
						dominantCount = counts[color]
					}
				}
			}
			if count > 0 {
				avg := runtimeFlairRGB(uint8(r/count), uint8(g/count), uint8(b/count))
				if mode == officeDownsampleCrisp {
					avg = officeCrispDownsampleColor(avg, dominant, dominantCount, count)
				}
				out.set(x, y, avg)
			}
		}
	}
	return out
}

func officeCrispDownsampleColor(avg, dominant runtimeFlairColor, dominantCount, count int) runtimeFlairColor {
	if count <= 0 || !avg.valid {
		return avg
	}
	share := (dominantCount * 100) / count
	tuned := avg
	if dominant.valid {
		switch {
		case share >= 72:
			tuned = officeBlendRuntimeColor(avg, dominant, 55)
		case share >= 48:
			tuned = officeBlendRuntimeColor(avg, dominant, 30)
		}
	}
	return officeBoostRuntimeColor(tuned, 112, 118)
}

func officeBlendRuntimeColor(a, b runtimeFlairColor, bPercent int) runtimeFlairColor {
	if !a.valid {
		return b
	}
	if !b.valid {
		return a
	}
	bPercent = max(0, min(100, bPercent))
	aPercent := 100 - bPercent
	return runtimeFlairRGB(
		uint8((int(a.r)*aPercent+int(b.r)*bPercent)/100),
		uint8((int(a.g)*aPercent+int(b.g)*bPercent)/100),
		uint8((int(a.b)*aPercent+int(b.b)*bPercent)/100),
	)
}

func officeBoostRuntimeColor(src runtimeFlairColor, contrastPct, saturationPct int) runtimeFlairColor {
	if !src.valid {
		return src
	}
	r := officeApplyContrast(int(src.r), contrastPct)
	g := officeApplyContrast(int(src.g), contrastPct)
	b := officeApplyContrast(int(src.b), contrastPct)
	luma := (r*30 + g*59 + b*11) / 100
	r = luma + ((r-luma)*saturationPct)/100
	g = luma + ((g-luma)*saturationPct)/100
	b = luma + ((b-luma)*saturationPct)/100
	return runtimeFlairRGB(uint8(officeClampByte(r)), uint8(officeClampByte(g)), uint8(officeClampByte(b)))
}

func officeApplyContrast(value, contrastPct int) int {
	return 128 + ((value-128)*contrastPct)/100
}

func officeClampByte(value int) int {
	return max(0, min(255, value))
}

func officeGameLongChatPanel(width, height int) string {
	body := []string{
		"You: what do we do about this branch?",
		"",
		"Assistant: I would keep this as a calm boss view, not a replacement",
		"for the detailed TUI. The point is to make attention feel spatial:",
		"desk for decisions, bench for active agents, shelf for parked work.",
		"",
		"Right now I see two decisions that need your taste. The coding",
		"agents can keep working, and the quiet shelf does not need a poke.",
		"",
		"I can also compact our long chat into durable memory, then search",
		"daily logs when we need the old context back.",
		"",
		"Suggested next move:",
		"  review the desk papers, then let me snooze the non-urgent items.",
		"",
		"> REVIEW DESK PAPERS",
		"> ASK MINA FOR OPTIONS",
		"> OPEN CLASSIC CONTROL ROOM",
	}
	return officeGameSoftPanel(width, height, "Assistant, chief of staff", body, officeCabinPanel, officeCabinAmber)
}

func officeGameAgendaPanel(width, height int) string {
	body := []string{
		"Next best move",
		"  Review desk papers",
		"",
		"Safe autopilot",
		"  Snooze quiet projects",
		"  Summarize agent drift",
		"",
		"Ask first",
		"  commit / push / kill process",
	}
	return officeGameSoftPanel(width, height, "Assistant tray", body, officeCabinPanel, officeCabinReview)
}

func officeGameDialogueRow(width, height int) string {
	leftW := min(max(50, (width*64)/100), width-28)
	rightW := max(20, width-leftW-1)
	if leftW+rightW+1 > width {
		rightW = max(18, width-leftW-1)
	}
	dialogue := officeGameDialoguePanel(leftW, height)
	hotspots := officeGameHotspotsPanel(rightW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, dialogue, lipgloss.NewStyle().Width(1).Background(officeCabinBG).Render(""), hotspots)
}

func officeGameDialoguePanel(width, height int) string {
	body := []string{
		"I'd start with the desk. Two decisions need your taste,",
		"and the workbench can keep running without interruption.",
		"",
		"I can tidy the obvious bits, but I will ask before risky moves.",
		"",
		"> REVIEW DESK PAPERS",
	}
	return officeGameSoftPanel(width, height, "Assistant, chief of staff", body, officeCabinPanel, officeCabinAmber)
}

func officeGameHotspotsPanel(width, height int) string {
	body := []string{
		"[R] Desk       2 decisions",
		"[W] Bench      3 agents active",
		"[L] Lantern    1 blocked",
		"[Q] Shelf      8 quiet",
		"",
		"Classic view stays one key away.",
	}
	return officeGameSoftPanel(width, height, "Room hotspots", body, officeCabinPanelDeep, officeCabinGreen)
}

func officeGameSoftPanel(width, height int, title string, body []string, bg, accent lipgloss.Color) string {
	width = max(8, width)
	height = max(2, height)
	titleStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinBG).Background(accent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinCream).Background(bg)
	mutedStyle := lipgloss.NewStyle().Width(width).Foreground(officeCabinMuted).Background(bg)

	lines := []string{titleStyle.Render(" " + truncateText(title, max(1, width-2)))}
	for _, line := range body {
		if len(lines) >= height {
			break
		}
		style := bodyStyle
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), ">") || strings.Contains(line, "blocked") || strings.Contains(line, "quiet") {
			style = mutedStyle
		}
		lines = append(lines, style.Render(" "+truncateText(line, max(1, width-2))))
	}
	for len(lines) < height {
		lines = append(lines, bodyStyle.Render(""))
	}
	return strings.Join(lines, "\n")
}

func officeGameVerbStrip(width int) string {
	verbs := " [L] LOOK   [T] TALK TO MINA   [R] REVIEW DESK   [D] DELEGATE SAFE CHORES   [S] SNOOZE   [TAB] CLASSIC "
	help := " room: cabin / outside river view     assistant memory: on     mode: high-level boss view "
	return strings.Join([]string{
		lipgloss.NewStyle().Width(width).Foreground(officeCabinAmber).Background(officeCabinWood).Bold(true).Render(truncateText(verbs, width)),
		lipgloss.NewStyle().Width(width).Foreground(officeCabinMuted).Background(officeCabinWood).Render(truncateText(help, width)),
	}, "\n")
}

func officeGameStatus(width int) string {
	text := " Trailblazer   Branch cabin-mockups   Agents 3 active   Decisions 2   Quiet shelf 8 "
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinGreen).
		Background(officeCabinPanelDeep).
		Render(truncateText(text, width))
}

func renderOfficeCabinTerminalStacked(width, height, phase int) string {
	sceneH := min(9, max(6, height/3))
	chatH := min(10, max(7, height-sceneH-8))
	lines := []string{
		officeTerminalHeader(width),
		officeTermBox(width, sceneH, "Cabin control room", officeTerminalScene(width-2, sceneH-2, phase), officeCabinPanelDeep, officeCabinWoodSoft),
		officeTermBox(width, chatH, "Assistant - your teammate [online]", []string{
			"Assistant: Four room objects need attention.",
			"You : Show me the review table.",
			"Assistant: Ready. I will keep the rest calm.",
			"",
			"> REVIEW [R] Review Table",
		}, officeCabinPanel, officeCabinAmber),
		officeCompactRow(width, "[R] Review Table", "2 decisions", officeCabinReview),
		officeCompactRow(width, "[W] Workbench", "3 active agents", officeCabinGreen),
		officeCompactRow(width, "[L] Lantern", "1 blocked", officeCabinWatch),
		officeFooterLine(width, "LOOK  TALK  REVIEW  SNOOZE  DELEGATE  CLASSIC"),
	}
	return strings.Join(lines, "\n")
}

func officeTerminalHeader(width int) string {
	left := " [^] "
	title := "Little Control Room"
	right := " Assistant awake  10:42 PM "
	space := max(1, width-len(left)-len(title)-len(right))
	text := left + strings.Repeat(" ", space/2) + title + strings.Repeat(" ", space-space/2) + right
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinAmber).
		Background(officeCabinPanelDeep).
		Bold(true).
		Render(truncateText(text, width))
}

func officeTerminalScene(width, height, phase int) []string {
	width = max(20, width)
	height = max(5, height)
	flicker := "*"
	if phase%2 == 1 {
		flicker = "+"
	}
	raw := []string{
		"        /\\                 [L]",
		"   ____/  \\____          " + flicker + " lamp",
		"  /  cabin ops \\       +--------+",
		" |  o  window  |      | shelf  |",
		" | [Assistant] |      | [Q][Q] |",
		" | desk  >_    |      +--------+",
		" | [R] papers  |    stove ####",
		" | [W] bench   |          ####",
		" +-------------+    rug ======",
	}
	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(raw) {
			line = raw[i]
		}
		lines = append(lines, line)
	}
	return lines
}

func officeTerminalSpotLines(width int) []string {
	rightPad := func(label, count string) string {
		room := max(1, width-len(label)-len(count)-2)
		return label + strings.Repeat(" ", room) + count + " >"
	}
	return []string{
		rightPad("[R] REVIEW TABLE", "[2]"),
		"    Decisions need your attention.",
		"",
		rightPad("[W] WORKBENCH", "[3]"),
		"    Active coding agents at work.",
		"",
		rightPad("[L] LANTERN", "[1]"),
		"    Blocked or waiting items.",
		"",
		rightPad("[Q] QUIET SHELF", "[2]"),
		"    Snoozed / calm items.",
	}
}

func officeTermBox(width, height int, title string, body []string, bg, accent lipgloss.Color) string {
	width = max(4, width)
	height = max(3, height)
	innerW := max(1, width-2)
	borderStyle := lipgloss.NewStyle().Foreground(accent).Background(bg)
	bodyStyle := lipgloss.NewStyle().Foreground(officeCabinCream).Background(bg)
	mutedStyle := lipgloss.NewStyle().Foreground(officeCabinMuted).Background(bg)

	topTitle := " " + truncateText(title, max(1, innerW-2)) + " "
	topFill := max(0, innerW-len(topTitle))
	lines := []string{
		borderStyle.Width(width).Render("+" + topTitle + strings.Repeat("-", topFill) + "+"),
	}
	for _, line := range body {
		if len(lines) >= height-1 {
			break
		}
		style := bodyStyle
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ">") || strings.Contains(trimmed, "snoozed") || strings.Contains(trimmed, "blocked") {
			style = mutedStyle
		}
		lines = append(lines, borderStyle.Render("|")+style.Width(innerW).Render(truncateText(line, innerW))+borderStyle.Render("|"))
	}
	blank := borderStyle.Render("|") + bodyStyle.Width(innerW).Render("") + borderStyle.Render("|")
	for len(lines) < height-1 {
		lines = append(lines, blank)
	}
	lines = append(lines, borderStyle.Width(width).Render("+"+strings.Repeat("-", innerW)+"+"))
	return strings.Join(lines, "\n")
}

type officeVerb struct {
	Key  string
	Name string
	Help string
}

func officeVerbBar(width int) string {
	items := []officeVerb{
		{Key: "L", Name: "LOOK", Help: "See around"},
		{Key: "T", Name: "TALK", Help: "Boss chat"},
		{Key: "R", Name: "REVIEW", Help: "Check decisions"},
		{Key: "S", Name: "SNOOZE", Help: "Rest for later"},
		{Key: "D", Name: "DELEGATE", Help: "Assign work"},
		{Key: "C", Name: "CLASSIC", Help: "Old mode"},
		{Key: "X", Name: "EXIT", Help: "Log off"},
	}
	n := len(items)
	widths := distributeTerminalWidths(max(n, width-(n+1)), n)
	border := officeVerbBorder(widths)
	nameRow := officeVerbRow(widths, items, func(item officeVerb, cellW int) string {
		return truncateText("["+item.Key+"] "+item.Name, cellW)
	})
	helpRow := officeVerbRow(widths, items, func(item officeVerb, cellW int) string {
		return truncateText(" "+item.Help, cellW)
	})
	style := lipgloss.NewStyle().Foreground(officeCabinAmber).Background(officeCabinWood).Bold(true)
	helpStyle := lipgloss.NewStyle().Foreground(officeCabinCream).Background(officeCabinWood)
	borderStyle := lipgloss.NewStyle().Foreground(officeCabinWoodSoft).Background(officeCabinWood)
	return strings.Join([]string{
		borderStyle.Width(width).Render(border),
		style.Width(width).Render(nameRow),
		helpStyle.Width(width).Render(helpRow),
		borderStyle.Width(width).Render(border),
	}, "\n")
}

func distributeTerminalWidths(total, count int) []int {
	count = max(1, count)
	base := total / count
	extra := total % count
	widths := make([]int, count)
	for i := range widths {
		widths[i] = base
		if i < extra {
			widths[i]++
		}
	}
	return widths
}

func officeVerbBorder(widths []int) string {
	var b strings.Builder
	b.WriteString("+")
	for _, width := range widths {
		b.WriteString(strings.Repeat("-", max(1, width)))
		b.WriteString("+")
	}
	return b.String()
}

func officeVerbRow(widths []int, items []officeVerb, render func(officeVerb, int) string) string {
	var b strings.Builder
	b.WriteString("|")
	for i, item := range items {
		cellW := widths[i]
		cell := render(item, cellW)
		b.WriteString(cell)
		if pad := cellW - len(cell); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString("|")
	}
	return b.String()
}

func officeStatusBar(width int) string {
	text := " Project: Trailblazer  |  Branch: cabin-mockups  |  Agents: 3 active  |  Mood: cozy  |  Classic: tab "
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinGreen).
		Background(officeCabinPanelDeep).
		Render(truncateText(text, max(1, width)))
}

func officeAdventureScene(width, height, phase int) string {
	width = max(24, width)
	height = max(5, height)
	canvasHeight := height * 2
	canvas := newRuntimeFlairCanvas(width, canvasHeight)

	wall := runtimeFlairRGB(18, 30, 24)
	wallShadow := runtimeFlairRGB(10, 18, 15)
	windowFrame := runtimeFlairRGB(84, 60, 39)
	windowSky := runtimeFlairRGB(22, 55, 59)
	mountain := runtimeFlairRGB(88, 130, 95)
	river := runtimeFlairRGB(72, 164, 150)
	floor := runtimeFlairRGB(45, 32, 23)
	floorEdge := runtimeFlairRGB(93, 67, 42)
	lamp := runtimeFlairRGB(236, 183, 79)
	redLamp := runtimeFlairRGB(224, 94, 78)
	paper := runtimeFlairRGB(235, 222, 184)

	canvas.fillRect(0, 0, width, canvasHeight, wall)
	canvas.fillRect(0, canvasHeight-2, width, 2, floor)
	canvas.fillRect(0, canvasHeight-3, width, 1, floorEdge)

	windowW := min(width-4, max(18, width/2))
	windowH := min(max(6, canvasHeight/3), max(4, canvasHeight-8))
	canvas.drawOutlinedRect(2, 2, windowW, windowH, windowFrame, windowSky)
	for i := 0; i < windowW-3; i += 6 {
		x := 4 + i
		base := 2 + windowH - 2
		canvas.fillRect(x, base-2, 1, 2, mountain)
		canvas.fillRect(x+1, base-3, 1, 3, mountain)
		canvas.fillRect(x+2, base-4, 1, 4, mountain)
		canvas.fillRect(x+3, base-3, 1, 3, mountain)
	}
	for i := 0; i < windowW-4; i++ {
		if (i+phase)%3 == 0 {
			canvas.set(4+i, 2+windowH-2, river)
		}
	}

	shelfX := max(4, width-13)
	shelfY := max(3, canvasHeight/3)
	canvas.fillRect(shelfX, shelfY, 10, 1, windowFrame)
	canvas.fillRect(shelfX+1, shelfY-2, 2, 2, paper)
	canvas.fillRect(shelfX+4, shelfY-3, 2, 3, runtimeFlairSignalGoodColor)
	canvas.fillRect(shelfX+7, shelfY-2, 2, 2, runtimeFlairMonitorDimColor)

	deskW := min(18, max(12, width/3))
	deskX := max(8, min(width-deskW-4, width/2-3))
	deskY := max(7, canvasHeight-8)
	runtimeFlairDrawDesk(&canvas, deskX, deskY, deskW, phase%runtimeFlairCycleSize)
	canvas.fillRect(deskX+2, deskY-2, 4, 1, paper)
	canvas.fillRect(deskX+7, deskY-2, 3, 1, paper)

	operatorY := max(2, canvasHeight-16)
	runtimeFlairDrawOperator(&canvas, runtimeFlairOperatorState{
		x:      max(2, deskX-12),
		y:      operatorY,
		facing: 1,
		pose:   runtimeFlairOperatorInspect,
		blink:  phase%4 == 1,
	})

	lampX := min(width-5, deskX+deskW+2)
	lampY := max(3, deskY-6)
	canvas.fillRect(lampX, lampY, 2, 2, lamp)
	canvas.fillRect(lampX, lampY+3, 2, 1, redLamp)
	canvas.fillRect(lampX+1, lampY+2, 1, 2, wallShadow)

	return canvas.render()
}

func officePanel(width, height int, title string, body []string, bg lipgloss.Color) string {
	width = max(1, width)
	height = max(1, height)
	titleStyle := lipgloss.NewStyle().Foreground(officeCabinAmber).Background(bg).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(officeCabinCream).Background(bg)
	mutedStyle := lipgloss.NewStyle().Foreground(officeCabinMuted).Background(bg)

	lines := []string{titleStyle.Width(width).Render(" " + truncateText(title, max(1, width-2)))}
	for _, line := range body {
		if len(lines) >= height {
			break
		}
		style := bodyStyle
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ">") || strings.Contains(trimmed, "not urgent") {
			style = mutedStyle
		}
		lines = append(lines, style.Width(width).Render(" "+truncateText(line, max(1, width-2))))
	}
	blank := bodyStyle.Width(width).Render("")
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

func officeScenePanel(width, height int, title, scene string, bg lipgloss.Color) string {
	width = max(1, width)
	height = max(1, height)
	titleStyle := lipgloss.NewStyle().Foreground(officeCabinAmber).Background(bg).Bold(true)
	bodyStyle := lipgloss.NewStyle().Background(bg)

	lines := []string{titleStyle.Width(width).Render(" " + truncateText(title, max(1, width-2)))}
	sceneLines := splitLines(scene)
	for _, line := range sceneLines {
		if len(lines) >= height {
			break
		}
		lines = append(lines, fitStyledWidth(line, width))
	}
	blank := bodyStyle.Width(width).Render("")
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, "\n")
}

func officeHeaderLine(width int, title, subtitle string) string {
	text := fmt.Sprintf("  %s  /  %s", title, subtitle)
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinCream).
		Background(officeCabinWood).
		Bold(true).
		Render(text)
}

func officeWindow(width, height, phase int) string {
	innerW := max(18, width-4)
	scene := officeWindowScene(innerW, max(3, height-2), phase)
	borderStyle := lipgloss.NewStyle().Foreground(officeCabinWoodSoft).Background(officeCabinBG)
	bodyStyle := lipgloss.NewStyle().Foreground(officeCabinGreen).Background(officeCabinPanelDeep)
	lines := []string{
		borderStyle.Width(width).Render("+" + strings.Repeat("-", max(0, width-2)) + "+"),
	}
	for _, line := range scene {
		lines = append(lines, borderStyle.Render("| ")+bodyStyle.Width(innerW).Render(line)+borderStyle.Render(" |"))
	}
	lines = append(lines, borderStyle.Width(width).Render("+"+strings.Repeat("-", max(0, width-2))+"+"))
	return strings.Join(lines, "\n")
}

func officeWindowScene(width, height, phase int) []string {
	lines := make([]string, 0, height)
	riverOffset := phase % 3
	for row := 0; row < height; row++ {
		var text string
		switch row {
		case 0:
			text = "     /\\        /\\             dusky trees beyond the glass"
		case 1:
			text = " /\\ /  \\  /\\  /  \\      " + strings.Repeat(" ", riverOffset) + "~ ~ river ~ ~"
		case 2:
			text = "/  V    \\/  \\/    \\          " + strings.Repeat("~ ", max(2, width/18))
		default:
			text = "       soft green outside" + strings.Repeat(" ", row%3) + "      lamp-warm cabin inside"
		}
		lines = append(lines, truncateText(text, width))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func officeAssistantBrief(width, phase int) []string {
	sprite := "[o_o]"
	if phase%2 == 1 {
		sprite = "[-_-]"
	}
	name := lipgloss.NewStyle().Foreground(officeCabinAmber).Bold(true).Render("Assistant")
	avatar := lipgloss.NewStyle().Foreground(officeCabinCream).Background(officeCabinWoodSoft).Bold(true).Render(" " + sprite + " ")
	speech := lipgloss.NewStyle().Foreground(officeCabinCream).Render("I see 3 things worth your attention. Everything else can breathe.")
	if width < 72 {
		speech = lipgloss.NewStyle().Foreground(officeCabinCream).Render("3 things need you. The rest can breathe.")
	}
	row := fmt.Sprintf("  %s  %s  %s", avatar, name, speech)
	row = fitStyledWidth(row, width)
	caption := "chief-of-staff mode: explain, suggest, remember, ask before risky moves"
	if width < 72 {
		caption = "explains, remembers, asks before risky moves"
	}
	return []string{
		lipgloss.NewStyle().Width(width).Foreground(officeCabinCream).Background(officeCabinBG).Render(""),
		lipgloss.NewStyle().Width(width).Background(officeCabinBG).Render(row),
		lipgloss.NewStyle().Width(width).Foreground(officeCabinMuted).Background(officeCabinBG).Render("        " + truncateText(caption, max(1, width-8))),
	}
}

func officeDashboardZones(width int) []string {
	if width < 78 {
		return []string{
			officeCompactRow(width, "Review Table", "LittleControlRoom: pick cabin direction", officeCabinReview),
			officeCompactRow(width, "Workbench", "FractalMech: active sprite pass", officeCabinGreen),
			officeCompactRow(width, "Quiet Shelf", "Browser Automation: snoozed until Monday", officeCabinMuted),
		}
	}
	gap := "  "
	cardW := max(22, (width-lipgloss.Width(gap)*2)/3)
	cards := []string{
		officeCard(cardW, "Review Table", []string{
			"LittleControlRoom",
			"Needs taste call on boss view.",
			"",
			"ChatNext3 memory notes",
			"Keep as design reference.",
		}, officeCabinPanel),
		officeCard(cardW, "Workbench", []string{
			"FractalMech",
			"Agent active. Build bench lit.",
			"",
			"Runtime lane",
			"One local process healthy.",
		}, officeCabinPanelDeep),
		officeCard(cardW, "Quiet Shelf", []string{
			"Browser Automation",
			"Snoozed until Monday.",
			"",
			"8 quiet projects",
			"No fresh boss decision.",
		}, officeCabinPanel),
	}
	return splitLines(lipgloss.JoinHorizontal(lipgloss.Top, cards[0], gap, cards[1], gap, cards[2]))
}

func officeCard(width int, title string, body []string, bg lipgloss.Color) string {
	titleStyle := lipgloss.NewStyle().Foreground(officeCabinAmber).Background(bg).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(officeCabinCream).Background(bg)
	mutedStyle := lipgloss.NewStyle().Foreground(officeCabinMuted).Background(bg)
	lines := []string{
		titleStyle.Width(width).Render(" " + title),
	}
	for _, line := range body {
		style := bodyStyle
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "No ") || strings.Contains(line, "Snoozed") {
			style = mutedStyle
		}
		lines = append(lines, style.Width(width).Render(" "+truncateText(line, max(0, width-2))))
	}
	for len(lines) < 8 {
		lines = append(lines, bodyStyle.Width(width).Render(""))
	}
	return strings.Join(lines, "\n")
}

func officeCompactRow(width int, label, value string, accent lipgloss.Color) string {
	labelW := min(18, max(12, width/3))
	valueW := max(8, width-labelW-3)
	labelPart := lipgloss.NewStyle().
		Width(labelW).
		Foreground(officeCabinBG).
		Background(accent).
		Bold(true).
		Render(" " + truncateText(label, max(1, labelW-2)))
	valuePart := lipgloss.NewStyle().
		Width(valueW).
		Foreground(officeCabinCream).
		Background(officeCabinPanel).
		Render(" " + truncateText(value, max(1, valueW-2)))
	return lipgloss.NewStyle().Width(width).Background(officeCabinBG).Render(labelPart + " " + valuePart)
}

func officeFooterLine(width int, text string) string {
	return lipgloss.NewStyle().
		Width(width).
		Foreground(officeCabinMuted).
		Background(officeCabinWood).
		Render(" " + truncateText(text, max(1, width-2)))
}

func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
}

func officeFitContent(content string, width, height int) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, 0, height)
	blank := lipgloss.NewStyle().Width(width).Background(officeCabinBG).Render("")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			out = append(out, blank)
			continue
		}
		out = append(out, fitStyledWidth(line, width))
	}
	for len(out) < height {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}
