package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnclosingResourceTypeInText(t *testing.T) {
	content := strings.Join([]string{
		`resource "aws_s3_bucket_policy" "x" {`, // 1
		`  bucket = module.s3_bucket.bucket_id`, // 2
		`  policy = data.aws_iam_policy_document.x.json`, // 3
		`}`, // 4
	}, "\n")
	if got := enclosingResourceTypeInText(content, 3); got != "aws_s3_bucket_policy" {
		t.Fatalf("got %q", got)
	}
	if got := enclosingResourceTypeInText("locals {\n  a = 1\n}", 2); got != "" {
		t.Fatalf("expected empty for non-resource, got %q", got)
	}
}

func TestProposedArguments(t *testing.T) {
	f := Finding{
		Comment:    "Add `tags = var.common_tags` to ensure compliance.",
		Suggestion: "  tags = var.common_tags",
	}
	args := proposedArguments(f)
	if len(args) == 0 || args[0] != "tags" {
		t.Fatalf("expected tags, got %v", args)
	}
}

// TestValidateTechResourceArgumentsStripsTagsOnBucketPolicy reproduces the
// stackadapt-zoominfo false positive end-to-end: a tags suggestion on an
// aws_s3_bucket_policy must be stripped and demoted.
func TestValidateTechResourceArgumentsStripsTagsOnBucketPolicy(t *testing.T) {
	root := t.TempDir()
	rel := "terraform/s3/zoominfo/main.tf"
	body := strings.Join([]string{
		`resource "aws_s3_bucket_policy" "x" {`,
		`  bucket = module.s3_bucket.bucket_id`,
		`  policy = data.aws_iam_policy_document.x.json`,
		`}`,
	}, "\n")
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := []Finding{{
		Path:       rel,
		Line:       2,
		Severity:   SeverityWarning,
		Comment:    "Add `tags = var.common_tags` to the bucket policy for compliance.",
		Suggestion: "  bucket = module.s3_bucket.bucket_id\n  tags   = var.common_tags",
	}}
	out := validateTechResourceArguments(findings, nil, root)
	if out[0].Suggestion != "" {
		t.Fatalf("expected suggestion stripped, got %q", out[0].Suggestion)
	}
	if out[0].Severity != SeverityInfo {
		t.Fatalf("expected demotion to info, got %q", out[0].Severity)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "does not accept") {
		t.Fatalf("expected stripped reason, got %q", out[0].SuggestionStrippedReason)
	}
}

// TestValidateTechResourceArgumentsLeavesTaggableResource ensures the gate
// does not touch a resource that genuinely accepts tags.
func TestValidateTechResourceArgumentsLeavesTaggableResource(t *testing.T) {
	root := t.TempDir()
	rel := "m.tf"
	body := "resource \"aws_s3_bucket\" \"x\" {\n  bucket = \"b\"\n}"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := []Finding{{
		Path:       rel,
		Line:       2,
		Severity:   SeverityWarning,
		Comment:    "Add `tags = var.common_tags`.",
		Suggestion: "  tags = var.common_tags",
	}}
	out := validateTechResourceArguments(findings, nil, root)
	if out[0].Suggestion == "" || out[0].Severity != SeverityWarning {
		t.Fatalf("taggable resource should be untouched, got sev=%q sugg=%q", out[0].Severity, out[0].Suggestion)
	}
}

// TestValidateTechResourceArgumentsDiffFallback resolves the resource type
// from the diff post-image when no worktree file is available.
func TestValidateTechResourceArgumentsDiffFallback(t *testing.T) {
	diff := strings.Join([]string{
		`diff --git a/m.tf b/m.tf`,
		`new file mode 100644`,
		`--- /dev/null`,
		`+++ b/m.tf`,
		`@@ -0,0 +1,3 @@`,
		`+resource "aws_iam_role_policy_attachment" "x" {`,
		`+  role       = aws_iam_role.x.name`,
		`+}`,
	}, "\n")
	files := ParseDiff(diff)
	findings := []Finding{{
		Path:       "m.tf",
		Line:       2,
		Severity:   SeverityWarning,
		Comment:    "Set `tags` on this attachment.",
		Suggestion: "  role = aws_iam_role.x.name\n  tags = {}",
	}}
	out := validateTechResourceArguments(findings, files, "")
	if out[0].Suggestion != "" || out[0].Severity != SeverityInfo {
		t.Fatalf("expected diff-fallback strip+demote, got sev=%q sugg=%q", out[0].Severity, out[0].Suggestion)
	}
}

func TestValidateTechResourceArgumentsIgnoresNonHCL(t *testing.T) {
	findings := []Finding{{Path: "main.go", Line: 2, Severity: SeverityWarning, Comment: "add `tags`", Suggestion: "tags := 1"}}
	out := validateTechResourceArguments(findings, nil, "")
	if out[0].Severity != SeverityWarning {
		t.Fatalf("non-HCL file must be untouched")
	}
}
