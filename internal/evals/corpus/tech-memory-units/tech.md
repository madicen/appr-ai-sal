## Kubernetes (technology expert)

This repo deploys to Kubernetes. Resource quantities for memory use binary
SI suffixes (Ki, Mi, Gi). A bare decimal suffix like `M` means megabytes
(10^6), not mebibytes (2^20), and is almost always a mistake in a memory
limit — prefer `Mi`.
