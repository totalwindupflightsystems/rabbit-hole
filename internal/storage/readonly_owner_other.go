//go:build !unix

package storage

// fileOwnerName is unavailable on this platform, so the ownership guidance
// omits the owning user name.
func fileOwnerName(string) string { return "" }
