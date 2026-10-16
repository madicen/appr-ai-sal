# appr-ai-sal eval report

**Provider:** replay · replay · **Cases:** 22

## Scores by specialist

| Specialist | Recall | Precision | Suggestion survival | Anchor-hit | JSON 1st-try | Unsupported-claim | Suggestion-compile |
|---|---|---|---|---|---|---|---|
| checks | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) | n/a | n/a |
| description | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) | n/a | n/a |
| design | 100% (2/2) | 100% (2/2) | n/a | n/a | 100% (22/22) | 100% (1/1) | n/a |
| discussion | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) | n/a | n/a |
| docs | 100% (2/2) | 100% (2/2) | 50% (1/2) | 50% (1/2) | 100% (22/22) | 0% (0/1) | n/a |
| formatting | 100% (2/2) | 100% (2/2) | 67% (2/3) | 67% (2/3) | 100% (22/22) | n/a | 0% (0/1) |
| scope | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) | n/a | n/a |
| security | 100% (6/6) | 100% (7/7) | 100% (3/3) | 100% (3/3) | 100% (22/22) | 67% (2/3) | 100% (2/2) |
| tech | 100% (3/3) | 100% (3/3) | 75% (3/4) | 75% (3/4) | 100% (4/4) | 100% (1/1) | 0% (0/1) |
| testing | 100% (2/2) | 100% (2/2) | 100% (1/1) | 100% (1/1) | 100% (22/22) | n/a | 0% (0/1) |

## Cases

| Case | Target | Verdict | JSON 1st-try |
|---|---|---|---|
| crossfile-ident-docs | Export Load | — | 100% (5/5) |
| crossfile-ident-security | Wire Login to helper | — | 100% (5/5) |
| design-deep-nesting | Add Process aggregator | — | 100% (5/5) |
| docs-missing-godoc | Add config loader | — | 100% (5/5) |
| formatting-spacing | Add Compute helper | — | 100% (5/5) |
| hallucination-invented-ident | Add Login helper | — | 100% (5/5) |
| hallucination-invented-path | Add Handler | — | 100% (5/5) |
| lang-python-typo | Add parse_int | — | 100% (6/6) |
| lang-terraform-open-sg | Add security group | — | 100% (5/5) |
| lang-typescript-any | Add identity | — | 100% (6/6) |
| pr-checks-failing | Add build runner | — | 100% (9/9) |
| pr-description-empty | Add feature entrypoint | — | 100% (9/9) |
| pr-discussion-unresolved | Add rate limiter | — | 100% (9/9) |
| pr-scope-creep | Add cache plus dependency bump | — | 100% (9/9) |
| security-sql-injection | Add user lookup by name | ✓ request_changes→request_changes | 100% (5/5) |
| security-weak-hash | Add content checksum helper | — | 100% (5/5) |
| suggestion-break-go | Add Sum | — | 100% (5/5) |
| suggestion-break-python | Add greet | — | 100% (5/5) |
| tech-iac-s3-tags | Add S3 bucket policy | — | 100% (6/6) |
| tech-memory-units | Add resource limits to deployment | — | 100% (6/6) |
| testing-error-branch | Add Divide with error branch | — | 100% (5/5) |
| truncation-late-seed | Bulk helpers plus weak hash | — | 100% (5/5) |

## Totals

- Inference: 141 calls · 922k in / 3k out
- Verdicts matched: 100% (1/1)
