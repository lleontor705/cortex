//go:build !windows

package obsidian

func isReparsePoint(string) bool { return false }
