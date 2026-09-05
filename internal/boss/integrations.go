package boss

import "lcroom/internal/integrations"

func (a *Assistant) integrationManager() *integrations.Manager {
	options := integrations.Options{DataDir: a.dataDir}
	if a.query != nil {
		options.CodexHome = a.query.codexHome
	}
	return integrations.New(options)
}
