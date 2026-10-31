resource "github_organization_members" "example" {
  members {
    username = "SomeUser"
    role     = "admin"
  }

  members {
    username = "AnotherUser"
    role     = "member"
  }
}
