//go:build windows

package obsidian

import "golang.org/x/sys/windows"

func isReparsePoint(path string) bool {
	attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
	return err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
