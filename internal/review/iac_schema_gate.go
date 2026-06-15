package review

import (
	"path/filepath"
	"regexp"
	"strings"
)

// unsupportedHCLArguments maps a Terraform resource type to the set of
// arguments that resource does NOT accept, keyed lowercase. It is the
// deterministic schema knowledge the schema gate uses to catch findings that
// tell the author to add an argument the provider would reject at
// `terraform validate` time.
//
// The seed set is the family of AWS resources that are commonly — but
// wrongly — told to "add tags": policy attachments, ACL/ownership/versioning
// sub-resources, and the bucket-policy resource that triggered the
// stackadapt-zoominfo false positive. None of these accept `tags` /
// `tags_all`; tagging belongs on the parent taggable resource (or the
// provider `default_tags` block). The map is intentionally small and
// high-confidence — it is a backstop for the model, not a full schema, and
// every entry should be one where adding the argument is unambiguously a
// `terraform validate` error.
var unsupportedHCLArguments = map[string]map[string]bool{
	"aws_s3_bucket_policy":              tagsUnsupported(),
	"aws_s3_bucket_acl":                 tagsUnsupported(),
	"aws_s3_bucket_ownership_controls":  tagsUnsupported(),
	"aws_s3_bucket_versioning":          tagsUnsupported(),
	"aws_s3_bucket_public_access_block": tagsUnsupported(),
	"aws_s3_bucket_server_side_encryption_configuration": tagsUnsupported(),
	"aws_s3_bucket_lifecycle_configuration":              tagsUnsupported(),
	"aws_iam_role_policy":                                tagsUnsupported(),
	"aws_iam_role_policy_attachment":                     tagsUnsupported(),
	"aws_iam_policy_attachment":                          tagsUnsupported(),
	"aws_iam_user_policy":                                tagsUnsupported(),
	"aws_iam_group_policy":                               tagsUnsupported(),
	"aws_lambda_permission":                              tagsUnsupported(),
}

func tagsUnsupported() map[string]bool {
	return map[string]bool{"tags": true, "tags_all": true}
}

// resourceDeclRe matches a Terraform resource block header, capturing the
// resource type. Whitespace-tolerant; the type is the first quoted string.
var resourceDeclRe = regexp.MustCompile(`^\s*resource\s+"([^"]+)"`)

// commentArgRe pulls the argument name out of comment phrasings that tell the
// author to add/set an argument: "add `tags`", "set tags =", "missing the
// tags argument", etc. Conservative: the argument must be a bare HCL
// identifier and must sit next to an add/set/include/missing/require/need cue
// or be written as an assignment.
var (
	commentAddArgRe    = regexp.MustCompile("(?i)\\b(?:add|set|include|missing|require[sd]?|needs?)\\b[^.\\n]{0,40}?`?([a-z][a-z0-9_]*)`?\\s*(?:=|argument|attribute|block)")
	commentAssignArgRe = regexp.MustCompile("(?im)^[^.\\n]{0,40}?`?([a-z][a-z0-9_]*)`?\\s*=\\s*[A-Za-z0-9_\"{]")
)

// validateTechResourceArguments strips suggestions and demotes findings that
// tell the author to add an argument the enclosing Terraform resource type
// does not accept. It is the schema backstop the architecture was missing:
// the structural suggestion gate (validateAndPruneSuggestions) catches
// "this would break the file's shape", but not "this argument doesn't exist
// on this resource" — which is exactly the stackadapt-zoominfo false
// positive (`tags = var.common_tags` on `aws_s3_bucket_policy`).
//
// It is intentionally conservative — it only acts when ALL of:
//   - the finding anchors to an HCL file (`.tf` / `.hcl`),
//   - the enclosing resource type can be resolved (from the worktree file,
//     falling back to the diff's post-image), and
//   - an argument the finding wants to add is listed as unsupported for that
//     resource type in unsupportedHCLArguments.
//
// When it acts it: clears any Suggestion (recording SuggestionStrippedReason,
// since a one-click apply would produce invalid Terraform), demotes severity
// to info (the prose may still be a useful nudge, but it must not block
// merge), and records ActionabilityNote so the TUI can explain why.
//
// Operates on findings in place; returns the same slice for ergonomics.
// worktree may be "" (tests / detached runs) — the gate then relies on the
// diff post-image alone.
func validateTechResourceArguments(findings []Finding, files []FileDiff, worktree string) []Finding {
	for i := range findings {
		f := &findings[i]
		path := strings.TrimSpace(f.Path)
		if path == "" || f.Line <= 0 || !isHCLPath(path) {
			continue
		}
		rtype := resolveEnclosingResourceType(path, f.Line, files, worktree)
		if rtype == "" {
			continue
		}
		unsupported, ok := unsupportedHCLArguments[rtype]
		if !ok {
			continue
		}
		bad := firstUnsupportedArg(*f, unsupported)
		if bad == "" {
			continue
		}
		note := "schema mismatch: the `" + rtype + "` resource does not accept a `" + bad + "` argument (would fail terraform validate)"
		switch f.Severity {
		case SeverityWarning, SeverityError, SeverityCritical:
			f.Severity = SeverityInfo
		}
		f.ActionabilityNote = note
		if strings.TrimSpace(f.Suggestion) != "" {
			f.Suggestion = ""
			f.SuggestionStrippedReason = note
		}
	}
	return findings
}

func isHCLPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tf", ".hcl":
		return true
	}
	return false
}

// firstUnsupportedArg returns the first argument the finding proposes adding
// that appears in the unsupported set, or "" when none do. Candidates come
// from both the machine-applicable Suggestion and the prose comment.
func firstUnsupportedArg(f Finding, unsupported map[string]bool) string {
	for _, arg := range proposedArguments(f) {
		if unsupported[arg] {
			return arg
		}
	}
	return ""
}

// proposedArguments collects the lowercase argument names the finding wants
// to add, from the suggestion's assignment lines and the comment's add/set
// phrasings.
func proposedArguments(f Finding) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(a string) {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			return
		}
		if _, ok := seen[a]; ok {
			return
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	for _, line := range strings.Split(f.Suggestion, "\n") {
		if m := commentAssignArgRe.FindStringSubmatch(line); len(m) == 2 {
			add(m[1])
		}
	}
	for _, m := range commentAddArgRe.FindAllStringSubmatch(f.Comment, -1) {
		if len(m) == 2 {
			add(m[1])
		}
	}
	for _, m := range commentAssignArgRe.FindAllStringSubmatch(f.Comment, -1) {
		if len(m) == 2 {
			add(m[1])
		}
	}
	return out
}

// resolveEnclosingResourceType returns the Terraform resource type whose
// block contains the given 1-indexed post-image line. It prefers the file on
// disk in the worktree (authoritative, full context) and falls back to the
// diff's post-image when the file isn't available.
func resolveEnclosingResourceType(path string, line int, files []FileDiff, worktree string) string {
	if worktree != "" {
		if content := readFileCapped(filepath.Join(worktree, filepath.FromSlash(path)), techEvidencePerFileRead); content != "" {
			if t := enclosingResourceTypeInText(content, line); t != "" {
				return t
			}
		}
	}
	if file := FindFile(files, path); file != nil {
		return enclosingResourceTypeFromDiff(file, line)
	}
	return ""
}

// enclosingResourceTypeInText scans upward from the target line over the full
// file text and returns the type of the nearest enclosing `resource "TYPE"`
// declaration. Returns "" when the line is above any resource block.
func enclosingResourceTypeInText(content string, line int) string {
	lines := strings.Split(content, "\n")
	idx := line - 1
	if idx >= len(lines) {
		idx = len(lines) - 1
	}
	for i := idx; i >= 0; i-- {
		if m := resourceDeclRe.FindStringSubmatch(lines[i]); len(m) == 2 {
			return m[1]
		}
	}
	return ""
}

// enclosingResourceTypeFromDiff scans the file's post-image (added + context
// lines) upward from the target line for the nearest resource declaration.
// Used when the worktree file is unavailable.
func enclosingResourceTypeFromDiff(file *FileDiff, line int) string {
	byLine := map[int]string{}
	maxNo := 0
	for hi := range file.Hunks {
		for _, l := range file.Hunks[hi].Lines {
			if l.Kind == DiffRemoved || l.NewNo == 0 {
				continue
			}
			byLine[l.NewNo] = l.Text
			if l.NewNo > maxNo {
				maxNo = l.NewNo
			}
		}
	}
	start := line
	if start > maxNo {
		start = maxNo
	}
	for i := start; i >= 1; i-- {
		text, ok := byLine[i]
		if !ok {
			continue
		}
		if m := resourceDeclRe.FindStringSubmatch(text); len(m) == 2 {
			return m[1]
		}
	}
	return ""
}
