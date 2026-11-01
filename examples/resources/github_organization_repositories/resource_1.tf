resource "github_repository" "website" {
  name = "website"
}

resource "github_repository" "infra" {
  name = "infra"
}

resource "github_organization_repositories" "example" {
  repository_names = [
    github_repository.website.name,
    github_repository.infra.name,
  ]
}
