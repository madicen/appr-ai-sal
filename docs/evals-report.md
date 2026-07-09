# appr-ai-sal eval report

**Provider:** replay · replay · **Cases:** 12

## Scores by specialist

| Specialist | Recall | Precision | Suggestion survival | Anchor-hit | JSON 1st-try |
|---|---|---|---|---|---|
| checks | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) |
| description | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) |
| design | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (12/12) |
| discussion | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) |
| docs | 100% (1/1) | 100% (1/1) | 100% (1/1) | 100% (1/1) | 100% (12/12) |
| formatting | 100% (1/1) | 100% (1/1) | 50% (1/2) | 50% (1/2) | 100% (12/12) |
| scope | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (4/4) |
| security | 100% (2/2) | 100% (2/2) | 100% (1/1) | 100% (1/1) | 100% (12/12) |
| tech | 100% (1/1) | 100% (1/1) | 50% (1/2) | 50% (1/2) | 100% (2/2) |
| testing | 100% (1/1) | 100% (1/1) | n/a | n/a | 100% (12/12) |

## Cases

| Case | Target | Verdict | JSON 1st-try |
|---|---|---|---|
| design-deep-nesting | Add Process aggregator | — | 100% (5/5) |
| docs-missing-godoc | Add config loader | — | 100% (5/5) |
| formatting-spacing | Add Compute helper | — | 100% (5/5) |
| pr-checks-failing | Add build runner | — | 100% (9/9) |
| pr-description-empty | Add feature entrypoint | — | 100% (9/9) |
| pr-discussion-unresolved | Add rate limiter | — | 100% (9/9) |
| pr-scope-creep | Add cache plus dependency bump | — | 100% (9/9) |
| security-sql-injection | Add user lookup by name | ✓ request_changes→request_changes | 100% (5/5) |
| security-weak-hash | Add content checksum helper | — | 100% (5/5) |
| tech-iac-s3-tags | Add S3 bucket policy | — | 100% (6/6) |
| tech-memory-units | Add resource limits to deployment | — | 100% (6/6) |
| testing-error-branch | Add Divide with error branch | — | 100% (5/5) |

## Totals

- Inference: 85 calls · 523k in / 2k out
- Verdicts matched: 100% (1/1)
