## Terraform / AWS (technology expert)

This repo manages AWS infrastructure with Terraform. Every taggable resource
should carry the standard `tags = var.common_tags` block for cost allocation.
(Note: not all resources are taggable — sub-resources like bucket policies and
ACLs are not.)
