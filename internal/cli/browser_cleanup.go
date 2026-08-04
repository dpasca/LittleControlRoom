package cli

import (
	"context"
	"fmt"
	"os"

	"lcroom/internal/browserctl"
	"lcroom/internal/config"
)

func startManagedPlaywrightStateCleanup(ctx context.Context, cfg config.AppConfig) {
	go browserctl.RunManagedPlaywrightCleanup(
		ctx,
		cfg.DataDir,
		cfg.PlaywrightCleanupPolicy,
		func(_ browserctl.ManagedPlaywrightCleanupResult, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "managed Playwright state cleanup failed: %v\n", err)
			}
		},
	)
}
