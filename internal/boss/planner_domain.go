package boss

import (
	"fmt"
	"strings"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
)

const (
	bossPlannerDomainGeneral          = "general"
	bossPlannerDomainInspection       = "inspection"
	bossPlannerDomainProjectWork      = "project_work"
	bossPlannerDomainAgentTask        = "agent_task"
	bossPlannerDomainProjectLifecycle = "project_lifecycle"
	bossPlannerDomainSettings         = "settings"
	bossPlannerDomainGit              = "git"
	bossPlannerDomainGoal             = "goal"
)

func bossPlannerDomainStrings() []string {
	return []string{
		bossPlannerDomainGeneral,
		bossPlannerDomainInspection,
		bossPlannerDomainProjectWork,
		bossPlannerDomainAgentTask,
		bossPlannerDomainProjectLifecycle,
		bossPlannerDomainSettings,
		bossPlannerDomainGit,
		bossPlannerDomainGoal,
	}
}

func normalizeBossPlannerDomain(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case bossPlannerDomainInspection:
		return bossPlannerDomainInspection
	case bossPlannerDomainProjectWork:
		return bossPlannerDomainProjectWork
	case bossPlannerDomainAgentTask:
		return bossPlannerDomainAgentTask
	case bossPlannerDomainProjectLifecycle:
		return bossPlannerDomainProjectLifecycle
	case bossPlannerDomainSettings:
		return bossPlannerDomainSettings
	case bossPlannerDomainGit:
		return bossPlannerDomainGit
	case bossPlannerDomainGoal:
		return bossPlannerDomainGoal
	default:
		return bossPlannerDomainGeneral
	}
}

func bossPlannerDomainIsScoped(domain string) bool {
	return normalizeBossPlannerDomain(domain) != bossPlannerDomainGeneral
}

func validateBossActionForPlannerDomain(action bossAction, rawDomain string) error {
	domain := normalizeBossPlannerDomain(rawDomain)
	if domain == bossPlannerDomainGeneral {
		return nil
	}
	kind := normalizeBossActionKind(action.Kind)
	if kind == bossActionAnswer || bossActionIsReadOnlyQuery(kind) {
		return nil
	}
	switch kind {
	case bossActionProposeControl:
		capability := control.CapabilityName(strings.TrimSpace(action.ControlCapability))
		for _, allowed := range bossPlannerDomainControlCapabilities(domain) {
			if capability == allowed {
				return nil
			}
		}
		return fmt.Errorf("planner_domain=%s does not allow control capability %q", domain, capability)
	case bossActionProposeGoal:
		if domain != bossPlannerDomainAgentTask && domain != bossPlannerDomainGoal {
			return fmt.Errorf("planner_domain=%s does not allow goal proposals", domain)
		}
		switch strings.TrimSpace(action.GoalKind) {
		case bossrun.GoalKindAgentTaskCleanup, bossrun.GoalKindLCAgentTask:
			return nil
		default:
			return fmt.Errorf("planner_domain=%s does not allow goal kind %q", domain, action.GoalKind)
		}
	default:
		return fmt.Errorf("planner_domain=%s does not allow action kind %q", domain, kind)
	}
}
