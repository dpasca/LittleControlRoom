package brand

const (
	Name                         = "Little Control Room"
	ShortName                    = "LCRoom"
	Subtitle                     = "Control Center for AI Tasks"
	FullTitle                    = Name + " - " + Subtitle
	CLIName                      = "lcroom"
	DataDirName                  = ".little-control-room"
	LegacyDataDirName            = ".batondeck"
	ConfigFileName               = "config.toml"
	DBFileName                   = "little-control-room.sqlite"
	LegacyDBFileName             = "batondeck.sqlite"
	CommitModelEnvVar            = "LCROOM_COMMIT_MODEL"
	SessionClassifierModelEnvVar = "LCROOM_SESSION_CLASSIFIER_MODEL"
)
