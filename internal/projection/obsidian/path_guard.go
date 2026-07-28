package obsidian

import "os"

// unsafePathInfo centralizes the containment guard. The metadata predicate is
// injectable so tests can exercise file and directory reparse branches without
// requiring OS privileges or filesystem support.
func unsafePathInfo(path string, info os.FileInfo, reparse func(string) bool) bool {
	return info != nil && (info.Mode()&os.ModeSymlink != 0 || reparse(path))
}
