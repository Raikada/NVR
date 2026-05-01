package identity

import "os"

// openStat is a thin wrapper around os.Stat used by tests to query
// file modes. Production code uses os.Stat directly when it needs
// the same.
func openStat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
