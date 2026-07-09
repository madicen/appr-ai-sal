package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cannedProvidersSchema is a trimmed `terraform providers schema -json`
// document: aws_s3_bucket is taggable, aws_s3_bucket_policy is not.
const cannedProvidersSchema = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/hashicorp/aws": {
      "resource_schemas": {
        "aws_s3_bucket": {
          "version": 0,
          "block": {
            "attributes": {
              "bucket": {"type": "string"},
              "tags": {"type": ["map", "string"]},
              "tags_all": {"type": ["map", "string"]}
            },
            "block_types": {
              "logging": {}
            }
          }
        },
        "aws_s3_bucket_policy": {
          "version": 0,
          "block": {
            "attributes": {
              "bucket": {"type": "string"},
              "policy": {"type": "string"}
            }
          }
        }
      }
    }
  }
}`

func TestParseTerraformProvidersSchema(t *testing.T) {
	schema, err := parseTerraformProvidersSchema([]byte(cannedProvidersSchema))
	if err != nil {
		t.Fatal(err)
	}
	bucket := schema["aws_s3_bucket"]
	if bucket == nil || !bucket["tags"] || !bucket["logging"] {
		t.Fatalf("aws_s3_bucket should accept tags + logging block: %v", bucket)
	}
	policy := schema["aws_s3_bucket_policy"]
	if policy == nil || policy["tags"] {
		t.Fatalf("aws_s3_bucket_policy must NOT accept tags: %v", policy)
	}
	if _, err := parseTerraformProvidersSchema([]byte("{}")); err == nil {
		t.Fatalf("empty schema should error (no resource schemas)")
	}
	if _, err := parseTerraformProvidersSchema([]byte("garbage")); err == nil {
		t.Fatalf("garbage should error")
	}
}

// withCannedSchema installs a fake live-schema source for a worktree key and
// clears the per-worktree cache so the fake is used, restoring both on cleanup.
func withCannedSchema(t *testing.T, schema map[string]map[string]bool) {
	t.Helper()
	prev := terraformFetchSchema
	terraformFetchSchema = func(worktree string) map[string]map[string]bool { return schema }
	tfSchemaCache.Range(func(k, _ any) bool { tfSchemaCache.Delete(k); return true })
	t.Cleanup(func() {
		terraformFetchSchema = prev
		tfSchemaCache.Range(func(k, _ any) bool { tfSchemaCache.Delete(k); return true })
	})
}

// TestFirstSchemaRejectedArgLive: with live schema present, a tags suggestion
// on a non-taggable resource is rejected via the live path, while a taggable
// resource is left alone.
func TestFirstSchemaRejectedArgLive(t *testing.T) {
	schema, err := parseTerraformProvidersSchema([]byte(cannedProvidersSchema))
	if err != nil {
		t.Fatal(err)
	}
	withCannedSchema(t, schema)

	f := Finding{Comment: "Add `tags = var.common_tags`.", Suggestion: "  tags = var.common_tags"}
	arg, live := firstSchemaRejectedArg(f, "aws_s3_bucket_policy", "/wt")
	if arg != "tags" || !live {
		t.Fatalf("expected live rejection of tags, got arg=%q live=%v", arg, live)
	}
	// Taggable resource: live schema accepts tags → nothing rejected.
	arg, live = firstSchemaRejectedArg(f, "aws_s3_bucket", "/wt")
	if arg != "" || !live {
		t.Fatalf("taggable resource: expected no rejection via live path, got arg=%q live=%v", arg, live)
	}
}

// TestFirstSchemaRejectedArgLiveGeneralizesBeyondTable: a resource NOT in the
// fallback table is still handled when live schema describes it — the whole
// point of Q5.c (schema covers all resources, table only 13).
func TestFirstSchemaRejectedArgLiveGeneralizesBeyondTable(t *testing.T) {
	schema := map[string]map[string]bool{
		"aws_vpc": {"cidr_block": true, "tags": true},
	}
	withCannedSchema(t, schema)
	if _, inTable := fallbackUnsupportedHCLArguments["aws_vpc"]; inTable {
		t.Fatalf("precondition: aws_vpc must not be in the fallback table")
	}
	f := Finding{Comment: "Add `enable_dns_supprt = true`.", Suggestion: "  enable_dns_supprt = true"}
	arg, live := firstSchemaRejectedArg(f, "aws_vpc", "/wt")
	if arg != "enable_dns_supprt" || !live {
		t.Fatalf("expected live rejection of typo'd arg on aws_vpc, got arg=%q live=%v", arg, live)
	}
}

// TestFirstSchemaRejectedArgFallbackTable: with NO live schema (terraform
// absent), the gate falls back to the static table exactly as before.
func TestFirstSchemaRejectedArgFallbackTable(t *testing.T) {
	withCannedSchema(t, nil) // fetch returns nil → fallback path
	f := Finding{Comment: "Add `tags = var.common_tags`.", Suggestion: "  tags = var.common_tags"}
	arg, live := firstSchemaRejectedArg(f, "aws_s3_bucket_policy", "/wt")
	if arg != "tags" || live {
		t.Fatalf("expected table fallback rejection (live=false), got arg=%q live=%v", arg, live)
	}
	// A resource not in the table and no live schema → nothing rejected.
	arg, _ = firstSchemaRejectedArg(f, "aws_s3_bucket", "/wt")
	if arg != "" {
		t.Fatalf("unknown resource with no live schema should not be rejected, got %q", arg)
	}
}

// TestValidateTechResourceArgumentsLivePathNote: the end-to-end gate strips +
// demotes via the live path and cites `terraform providers schema` in the note.
func TestValidateTechResourceArgumentsLivePathNote(t *testing.T) {
	schema, _ := parseTerraformProvidersSchema([]byte(cannedProvidersSchema))
	withCannedSchema(t, schema)

	root := t.TempDir()
	rel := "main.tf"
	body := strings.Join([]string{
		`resource "aws_s3_bucket_policy" "x" {`,
		`  bucket = module.s3.bucket_id`,
		`}`,
	}, "\n")
	writeTempFile(t, root, rel, body)

	findings := []Finding{{
		Path:       rel,
		Line:       2,
		Severity:   SeverityWarning,
		Comment:    "Add `tags = var.common_tags` for compliance.",
		Suggestion: "  bucket = module.s3.bucket_id\n  tags   = var.common_tags",
	}}
	out := validateTechResourceArguments(findings, nil, root)
	if out[0].Suggestion != "" || out[0].Severity != SeverityInfo {
		t.Fatalf("expected live-path strip+demote, got sev=%q sugg=%q", out[0].Severity, out[0].Suggestion)
	}
	if !strings.Contains(out[0].SuggestionStrippedReason, "terraform providers schema") {
		t.Fatalf("expected live-schema note, got %q", out[0].SuggestionStrippedReason)
	}
}

// TestValidateTechResourceArgumentsLiveAllowsTaggable: live schema says the
// resource IS taggable, so even though the model proposes tags the gate leaves
// the finding untouched (the live path overrides any table guess).
func TestValidateTechResourceArgumentsLiveAllowsTaggable(t *testing.T) {
	schema, _ := parseTerraformProvidersSchema([]byte(cannedProvidersSchema))
	withCannedSchema(t, schema)

	root := t.TempDir()
	rel := "b.tf"
	body := "resource \"aws_s3_bucket\" \"x\" {\n  bucket = \"b\"\n}"
	writeTempFile(t, root, rel, body)
	findings := []Finding{{
		Path:       rel,
		Line:       2,
		Severity:   SeverityWarning,
		Comment:    "Add `tags = var.common_tags`.",
		Suggestion: "  tags = var.common_tags",
	}}
	out := validateTechResourceArguments(findings, nil, root)
	if out[0].Suggestion == "" || out[0].Severity != SeverityWarning {
		t.Fatalf("taggable resource must be untouched, got sev=%q sugg=%q", out[0].Severity, out[0].Suggestion)
	}
}

func writeTempFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
