//go:build unix

package storage

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// fileOwnerName returns the user name owning path, or "" when it cannot be
// resolved (missing file, or a stat that carries no uid).
func fileOwnerName(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	uid := strconv.FormatUint(uint64(st.Uid), 10)
	// The user database may not know the uid (containers, numeric-only users);
	// report the numeric form rather than dropping the owner entirely.
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		return u.Username
	}
	return "uid " + uid
}
