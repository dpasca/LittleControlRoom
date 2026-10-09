package cli

import (
	"context"
	"log"

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
				log.Printf("managed Playwright state cleanup failed: %v", err)
			}
		},
	)
}
