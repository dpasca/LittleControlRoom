package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/demoedit"
	"lcroom/internal/demorecord"

	tea "github.com/charmbracelet/bubbletea"
)

func runDemo(programName string, args []string) int {
	if len(args) == 0 {
		printDemoUsage(programName)
		return 2
	}
	switch strings.TrimSpace(args[0]) {
	case "record":
		return runDemoRecord(programName, args[1:])
	case "edit":
		return runDemoEdit(args[1:])
	case "play":
		return runDemoPlay(args[1:])
	case "export":
		return runDemoExport(args[1:])
	case "help", "--help", "-h":
		printDemoUsage(programName)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown demo command: %s\n", args[0])
		printDemoUsage(programName)
		return 2
	}
}

func runDemoPlay(args []string) int {
	if len(args) == 0 || strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		fmt.Fprintln(os.Stderr, "usage: lcroom demo play <recording.lcrdemo> [--clip <id|number>]")
		return 2
	}
	recordingPath := args[0]
	args = args[1:]
	remaining, clipSelector, err := stripPathFlagArg(args, "--clip")
	if err != nil {
		fmt.Fprintf(os.Stderr, "demo play flag error: %v\n", err)
		return 2
	}
	if len(remaining) > 0 {
		fmt.Fprintf(os.Stderr, "demo play does not recognize arguments: %s\n", strings.Join(remaining, " "))
		return 2
	}
	reader, err := demorecord.Open(recordingPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open demo recording: %v\n", err)
		return 1
	}
	manifest := reader.Manifest()
	clip := demorecord.Clip{
		ID:              "recording",
		Name:            "Recording",
		InMS:            0,
		OutMS:           manifest.DurationMS,
		IdleTimeLimitMS: 2000,
		SmartTiming:     true,
	}
	if strings.TrimSpace(clipSelector) != "" {
		edits, err := demorecord.LoadEdits(reader.Path())
		if err != nil {
			fmt.Fprintf(os.Stderr, "open demo edits: %v\n", err)
			return 1
		}
		clip, err = selectDemoClip(edits.Clips, clipSelector)
		if err != nil {
			fmt.Fprintf(os.Stderr, "select demo clip: %v\n", err)
			return 2
		}
	}
	model, err := demoedit.NewPlayer(reader, clip)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open demo playback: %v\n", err)
		return 1
	}
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "demo playback failed: %v\n", err)
		return 1
	}
	return 0
}

func runDemoRecord(programName string, args []string) int {
	outputPath := ""
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		outputPath = strings.TrimSpace(args[0])
		args = args[1:]
	}
	if outputPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve demo recording directory: %v\n", err)
			return 1
		}
		outputPath = filepath.Join(cwd, demorecord.DefaultRecordingName(time.Now()))
	}
	tuiArgs := []string{"tui", "--demo-record", outputPath}
	tuiArgs = append(tuiArgs, args...)
	return Run(programName, tuiArgs)
}

func runDemoEdit(args []string) int {
	if len(args) != 1 || strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		fmt.Fprintln(os.Stderr, "usage: lcroom demo edit <recording.lcrdemo>")
		return 2
	}
	reader, err := demorecord.Open(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open demo recording: %v\n", err)
		return 1
	}
	edits, err := demorecord.LoadEdits(reader.Path())
	if err != nil {
		fmt.Fprintf(os.Stderr, "open demo edits: %v\n", err)
		return 1
	}
	model, err := demoedit.New(reader, edits)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open demo editor: %v\n", err)
		return 1
	}
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "demo editor failed: %v\n", err)
		return 1
	}
	return 0
}

func runDemoExport(args []string) int {
	if len(args) == 0 || strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		fmt.Fprintln(os.Stderr, "usage: lcroom demo export <recording.lcrdemo> --clip <id|number> [--output <clip.cast>]")
		return 2
	}
	recordingPath := args[0]
	args = args[1:]
	clipSelector := ""
	outputPath := ""
	var err error
	args, clipSelector, err = stripPathFlagArg(args, "--clip")
	if err != nil {
		fmt.Fprintf(os.Stderr, "demo export flag error: %v\n", err)
		return 2
	}
	args, outputPath, err = stripPathFlagArg(args, "--output")
	if err != nil {
		fmt.Fprintf(os.Stderr, "demo export flag error: %v\n", err)
		return 2
	}
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "demo export does not recognize arguments: %s\n", strings.Join(args, " "))
		return 2
	}

	reader, err := demorecord.Open(recordingPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open demo recording: %v\n", err)
		return 1
	}
	edits, err := demorecord.LoadEdits(reader.Path())
	if err != nil {
		fmt.Fprintf(os.Stderr, "open demo edits: %v\n", err)
		return 1
	}
	clip, err := selectDemoClip(edits.Clips, clipSelector)
	if err != nil {
		fmt.Fprintf(os.Stderr, "select demo clip: %v\n", err)
		return 2
	}
	if strings.TrimSpace(outputPath) == "" {
		outputPath = demorecord.DefaultExportPath(reader.Path(), clip)
	}
	if err := demorecord.ExportAsciicast(reader, clip, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "export demo clip: %v\n", err)
		return 1
	}
	absolute, _ := filepath.Abs(filepath.Clean(outputPath))
	fmt.Printf("demo clip exported to %s\n", absolute)
	return 0
}

func selectDemoClip(clips []demorecord.Clip, selector string) (demorecord.Clip, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		if len(clips) == 1 {
			return clips[0], nil
		}
		if len(clips) == 0 {
			return demorecord.Clip{}, fmt.Errorf("no saved clips; use `lcroom demo edit` first")
		}
		return demorecord.Clip{}, fmt.Errorf("%d clips are saved; pass --clip", len(clips))
	}
	for _, clip := range clips {
		if clip.ID == selector {
			return clip, nil
		}
	}
	if index, err := strconv.Atoi(selector); err == nil && index >= 1 && index <= len(clips) {
		return clips[index-1], nil
	}
	return demorecord.Clip{}, fmt.Errorf("clip %q was not found", selector)
}

func printDemoUsage(programName string) {
	name := strings.TrimSpace(programName)
	if name == "" {
		name = "lcroom"
	}
	fmt.Println("Demo recording:")
	fmt.Printf("  %s demo record [recording.lcrdemo] [TUI flags]\n", name)
	fmt.Printf("  %s demo edit <recording.lcrdemo>\n", name)
	fmt.Printf("  %s demo play <recording.lcrdemo> [--clip <id|number>]\n", name)
	fmt.Printf("  %s demo export <recording.lcrdemo> --clip <id|number> [--output <clip.cast>]\n", name)
}
