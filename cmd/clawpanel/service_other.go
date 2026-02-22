//go:build !windows

package main

// runAsService is a no-op on non-Windows platforms.
func runAsService() bool {
	return false
}
