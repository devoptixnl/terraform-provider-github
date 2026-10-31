resource "github_team" "maintainers" {
  name = "maintainers"
}

resource "github_team" "contributors" {
  name = "contributors"
}

resource "github_organization_teams" "example" {
  team_slugs = [
    github_team.maintainers.slug,
    github_team.contributors.slug,
  ]
}
