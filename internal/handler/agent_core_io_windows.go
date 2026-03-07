//go:build windows

package handler

import (
	"time"
)

func loadAgentCoreFile(workspace agentCoreWorkspaceLocation, name string) (string, int64, time.Time, bool, error) {
	return "", 0, time.Time{}, false, errAgentCoreFileUnsupportedPlatform
}

func saveAgentCoreFile(workspace agentCoreWorkspaceLocation, name string, content []byte) error {
	return errAgentCoreFileUnsupportedPlatform
}
