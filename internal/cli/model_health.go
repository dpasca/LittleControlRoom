package cli

import (
	"fmt"
	"io"

	"lcroom/internal/config"
)

// reportModelMismatches prints every provider/model divergence found in the
// loaded config.
//
// This runs on every subcommand, immediately after the config is parsed, so a
// stale model is announced once at startup instead of surfacing later as an
// unexplained provider 400 in whichever feature happened to call it first. It
// reports rather than refuses: runtime routes that have a safe provider default
// correct known cross-provider values, while reporting keeps the UI accessible
// so inactive or manually managed fields can be fixed in place.
func reportModelMismatches(w io.Writer, cfg config.AppConfig) {
	for _, mismatch := range cfg.ModelMismatches() {
		fmt.Fprintf(w, "ERROR %s\n", mismatch.Detail())
	}
}
