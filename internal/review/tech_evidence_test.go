package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractTechTokens(t *testing.T) {
	got := extractTechTokens("Add `tags = var.common_tags` to the `aws_s3_bucket_policy` resource for compliance.")
	// var.common_tags must rank first (HCL reference), then backtick idents.
	if len(got) == 0 || got[0] != "var.common_tags" {
		t.Fatalf("expected var.common_tags first, got %v", got)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "aws_s3_bucket_policy") {
		t.Fatalf("expected backtick identifier captured, got %v", got)
	}
}

func TestExtractTechTokensEmpty(t *testing.T) {
	if got := extractTechTokens("please fix this"); len(got) != 0 {
		t.Fatalf("expected no tokens, got %v", got)
	}
}

func TestExtractTechTokensCaps(t *testing.T) {
	c := "`aaa` `bbb` `ccc` `ddd` `eee` `fff` `ggg` `hhh`"
	if got := extractTechTokens(c); len(got) > techEvidenceMaxTokens {
		t.Fatalf("token count %d exceeds cap %d", len(got), techEvidenceMaxTokens)
	}
}

func TestSiblingSearchRoot(t *testing.T) {
	cases := map[string]string{
		"terraform/acct/s3/zoominfo/main.tf": "terraform/acct/s3",
		"top/main.tf":                        "top",
		"main.tf":                            "",
	}
	for in, want := range cases {
		if got := siblingSearchRoot(in); got != want {
			t.Errorf("siblingSearchRoot(%q) = %q want %q", in, got, want)
		}
	}
}

// TestBuildTechConventionEvidence models the stackadapt-zoominfo false
// positive: the finding asks for `var.common_tags`, which no sibling module
// in the repo uses. The evidence must report 0/N for that token so the
// witness can mark the finding congruent (repo doesn't do this).
func TestBuildTechConventionEvidence(t *testing.T) {
	root := t.TempDir()
	mkfile := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The changed file plus two sibling modules that tag via the metadata
	// module / default_tags but never reference var.common_tags.
	mkfile("terraform/acct/s3/zoominfo/main.tf", "resource \"aws_s3_bucket_policy\" \"x\" {\n  bucket = module.s3_bucket.bucket_id\n}\n")
	mkfile("terraform/acct/s3/peopledatalabs/main.tf", "module \"s3_bucket\" {\n  context = module.this.context\n}\n")
	mkfile("terraform/acct/s3/geoupload/main.tf", "module \"s3_bucket\" {\n  context = module.this.context\n}\n")

	findings := []Finding{{
		Path:     "terraform/acct/s3/zoominfo/main.tf",
		Line:     1,
		Severity: SeverityWarning,
		Comment:  "Add `tags = var.common_tags` to the aws_s3_bucket_policy resource for compliance.",
	}}
	out := BuildTechConventionEvidence(root, findings)
	if out == "" {
		t.Fatal("expected non-empty evidence")
	}
	if !strings.Contains(out, "token `var.common_tags`: present in 0 of 2") {
		t.Fatalf("expected var.common_tags 0/2, got:\n%s", out)
	}
	if !strings.Contains(out, "sibling `.tf` file(s)") {
		t.Fatalf("expected sibling sample summary, got:\n%s", out)
	}
}

func TestBuildTechConventionEvidenceNoTokens(t *testing.T) {
	root := t.TempDir()
	if out := BuildTechConventionEvidence(root, []Finding{{Path: "a/main.tf", Line: 1, Comment: "looks off"}}); out != "" {
		t.Fatalf("expected empty for token-less finding, got %q", out)
	}
}

func TestAppendTechConventionEvidence(t *testing.T) {
	if got := appendTechConventionEvidence("base pack", "", nil); got != "base pack" {
		t.Fatalf("expected unchanged pack, got %q", got)
	}
}
