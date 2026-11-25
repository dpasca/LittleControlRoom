package model

import "time"

type ProjectBriefText struct {
	Goal  string             `json:"goal"`
	Now   string             `json:"now"`
	Needs *ProjectBriefNeeds `json:"needs"`
	Next  string             `json:"next"`
}

type ProjectBriefNeeds struct {
	Question       string             `json:"question"`
	Options        []string           `json:"options"`
	Recommendation string             `json:"recommendation"`
	Default        string             `json:"default"`
	Deadline       string             `json:"deadline"`
	Check          *ProjectBriefCheck `json:"check,omitempty"`
}

type ProjectBriefCheck struct {
	Kind         string `json:"kind"`
	TargetBranch string `json:"target_branch,omitempty"`
	RemoteBranch string `json:"remote_branch,omitempty"`
}

type ProjectBrief struct {
	ProjectBriefText
	CheckoutPath string        `json:"checkout_path"`
	Provider     SessionSource `json:"provider"`
	RecordedAt   time.Time     `json:"recorded_at"`
	NewerSession bool          `json:"newer_session"`
	GitChanged   *bool         `json:"git_changed"`
	NeedsStatus  string        `json:"needs_status"`
	Answer       string        `json:"answer,omitempty"`
}
