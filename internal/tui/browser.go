package tui

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// browserOpenedMsg is delivered after a browser-open attempt so the root
// model can surface failures via the existing error overlay. Successful
// opens fire-and-forget; we only care about the error path.
type browserOpenedMsg struct {
	URL string
	Err error
}

// browserOpenCommand returns the OS-appropriate exec.Cmd to open rawURL in
// the user's default browser, or an error if the URL is malformed or the
// host OS is unsupported. Split from openInBrowserCmd so the platform
// dispatch can be unit-tested without actually shelling out.
func browserOpenCommand(rawURL string) (*exec.Cmd, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("empty URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	// Only allow http(s) so a bad URL field never causes us to invoke a
	// custom protocol handler (e.g. file://, ssh://) the user didn't ask for.
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL), nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return exec.Command("xdg-open", rawURL), nil
	case "windows":
		// rundll32 avoids the cmd.exe quoting pitfalls of `start` while
		// still using the user's default browser handler.
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL), nil
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// openInBrowserCmd opens rawURL in the user's default browser. The returned
// tea.Cmd resolves to a browserOpenedMsg with Err set on failure (so the
// root model can route it through the error overlay) and nil on success.
func openInBrowserCmd(rawURL string) tea.Cmd {
	return func() tea.Msg {
		cmd, err := browserOpenCommand(rawURL)
		if err != nil {
			return browserOpenedMsg{URL: rawURL, Err: err}
		}
		if err := cmd.Start(); err != nil {
			return browserOpenedMsg{URL: rawURL, Err: fmt.Errorf("launch browser: %w", err)}
		}
		// Detach: we don't care when the GUI app exits, only that it
		// launched cleanly.
		_ = cmd.Process.Release()
		return browserOpenedMsg{URL: rawURL}
	}
}
