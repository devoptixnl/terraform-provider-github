locals {
  repositories = {
    website = "Company website"
    infra   = "Infrastructure as code"
  }
}

resource "github_repository" "this" {
  for_each = local.repositories

  name        = each.key
  description = each.value
}

resource "github_organization_repositories" "example" {
  repository_names = [for repo in github_repository.this : repo.name]
}
