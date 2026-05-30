package browserctl

import "context"

type BrowserSession interface {
	Navigate(context.Context, string) (BrowserActionResult, error)
	Snapshot(context.Context, int) (BrowserActionResult, error)
	Click(context.Context, string) (BrowserActionResult, error)
	Fill(context.Context, string, string) (BrowserActionResult, error)
	Press(context.Context, string) (BrowserActionResult, error)
	Screenshot(context.Context, string) (BrowserActionResult, error)
	CurrentPage(context.Context) (BrowserActionResult, error)
	Close() error
}

type BrowserActionResult struct {
	URL          string
	Title        string
	Status       string
	Snapshot     string
	ArtifactPath string
	Fresh        bool
}

type BrowserSessionConfig struct {
	DataDir        string
	Provider       string
	ProjectPath    string
	SessionKey     string
	ProfileKey     string
	LaunchMode     ManagedLaunchMode
	Policy         Policy
	BrowserChannel string
	BrowserPath    string
}
