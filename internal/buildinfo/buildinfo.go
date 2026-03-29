package buildinfo

import (
	"os"
	"strings"
)

var (
	Version = "dev"
	Edition = "pro"
	Branch  = "main"
	Commit  = ""
	Label   = ""
)

func NormalizedEdition() string {
	edition := strings.TrimSpace(strings.ToLower(os.Getenv("CLAWPANEL_EDITION")))
	if edition == "" {
		edition = strings.TrimSpace(strings.ToLower(Edition))
	}
	switch edition {
	case "lite":
		return "lite"
	default:
		return "pro"
	}
}

func IsLite() bool {
	return NormalizedEdition() == "lite"
}

func NormalizedBranch() string {
	branch := strings.TrimSpace(Branch)
	if branch == "" {
		return "main"
	}
	return branch
}

func ShortCommit() string {
	commit := strings.TrimSpace(Commit)
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func BuildLabel() string {
	label := strings.TrimSpace(Label)
	if label != "" {
		return label
	}
	parts := []string{NormalizedBranch()}
	if short := ShortCommit(); short != "" {
		parts = append(parts, short)
	}
	return strings.Join(parts, " @ ")
}
