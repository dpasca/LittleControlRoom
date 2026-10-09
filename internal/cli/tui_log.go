package cli

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// captureTUILog keeps background diagnostics away from the terminal while
// Bubble Tea owns it. A direct stderr write moves the cursor without updating
// the renderer's screen state, leaving stale rows such as duplicate prompts.
func captureTUILog(dataDir string) (func(), error) {
	dir := filepath.Join(dataDir, "crash-dumps")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, time.Now().Format("20060102-150405")+"-tui-*.log")
	if err != nil {
		return nil, err
	}
	previous := log.Writer()
	log.SetOutput(file)
	return func() {
		// Restore the writer before closing the file, after TUI/session shutdown.
		log.SetOutput(previous)
		info, statErr := file.Stat()
		closeErr := file.Close()
		if statErr == nil && info.Size() == 0 && closeErr == nil {
			_ = os.Remove(file.Name())
			return
		}
		fmt.Fprintf(os.Stderr, "TUI diagnostic log: %s\n", file.Name())
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "close TUI diagnostic log: %v\n", closeErr)
		}
	}, nil
}
