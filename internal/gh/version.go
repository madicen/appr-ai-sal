package gh

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// MinGHVersion is the lowest `gh` CLI version appr-ai-sal supports. We still
// shell out to `gh pr view/diff/list` and rely on go-gh resolving the same
// auth the CLI stores, so a too-old CLI can produce confusing failures
// (missing JSON fields, stale auth config formats). Enforcing a floor turns
// those into one clear "upgrade gh" message at startup.
const MinGHVersion = "2.0.0"

// ghVersionRe matches the dotted numeric version inside `gh --version` output,
// e.g. "gh version 2.40.1 (2023-12-13)".
var ghVersionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseGHVersion extracts the "MAJOR.MINOR.PATCH" version string from
// `gh --version` output, or "" when no version-looking token is present.
func parseGHVersion(out string) string {
	m := ghVersionRe.FindString(out)
	return m
}

// ghVersionAtLeast reports whether have >= min, comparing dotted numeric
// versions component-by-component. A version we can't parse (have == "")
// returns true so an unexpected `gh --version` format never locks the user
// out — we'd rather proceed and let a real API error surface than refuse over
// a formatting quirk.
func ghVersionAtLeast(have, min string) bool {
	if strings.TrimSpace(have) == "" {
		return true
	}
	return compareDottedVersions(have, min) >= 0
}

// compareDottedVersions returns -1, 0, or 1 comparing two dotted numeric
// version strings. Missing trailing components are treated as zero
// ("2.0" == "2.0.0"); non-numeric components compare as zero.
func compareDottedVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		}
	}
	return 0
}

// checkGHVersion runs `gh --version` and returns an error when the installed
// CLI is older than MinGHVersion. It fails open: if `gh --version` can't run
// or its output can't be parsed, it returns nil and lets the auth check (or a
// later API call) surface the real problem.
func checkGHVersion() error {
	out, err := exec.Command("gh", "--version").CombinedOutput()
	if err != nil {
		return nil
	}
	have := parseGHVersion(string(out))
	if !ghVersionAtLeast(have, MinGHVersion) {
		return fmt.Errorf("gh %s is older than the required %s; upgrade (e.g. `brew upgrade gh`) or reinstall from https://cli.github.com", have, MinGHVersion)
	}
	return nil
}
