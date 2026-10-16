//go:build windows

package sidebar

import "errors"

// Launch is not available on Windows, where tmux does not run; use the classic layout.
func Launch(sidebarCommand string) error {
	return errors.New("the sidebar layout needs tmux and is not available on Windows; run with --classic")
}
