package github

import (
	"fmt"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestOrganizationMemberChanges(t *testing.T) {
	t.Parallel()

	member := func(login, role string) userMember {
		return userMember{userIdentity: userIdentity{login: login}, role: role}
	}

	tests := []struct {
		name        string
		current     userMembers
		want        userMembers
		wantUpserts userMembers
		wantRemoved []string
	}{
		{
			name:    "no_changes",
			current: userMembers{member("owner", "admin"), member("dev", "member")},
			want:    userMembers{member("owner", "admin"), member("dev", "member")},
		},
		{
			name:        "removes_unlisted_members",
			current:     userMembers{member("owner", "admin"), member("zed", "member"), member("amy", "admin")},
			want:        userMembers{member("owner", "admin")},
			wantRemoved: []string{"amy", "zed"},
		},
		{
			name:        "adds_new_members",
			current:     userMembers{member("owner", "admin")},
			want:        userMembers{member("owner", "admin"), member("dev", "member")},
			wantUpserts: userMembers{member("dev", "member")},
		},
		{
			name:        "changes_roles",
			current:     userMembers{member("owner", "admin"), member("dev", "member")},
			want:        userMembers{member("owner", "member"), member("dev", "admin")},
			wantUpserts: userMembers{member("dev", "admin"), member("owner", "member")},
		},
		{
			name:    "ignores_username_case",
			current: userMembers{member("owner", "admin")},
			want:    userMembers{member("Owner", "admin")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			upserts, removed := organizationMemberChanges(tt.current, tt.want)

			if !slices.Equal(upserts, tt.wantUpserts) {
				t.Errorf("upserts = %v, want %v", upserts, tt.wantUpserts)
			}
			if !slices.Equal(removed, tt.wantRemoved) {
				t.Errorf("removed = %v, want %v", removed, tt.wantRemoved)
			}
		})
	}
}

func TestOrganizationInvitationRole(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"admin": "admin", "direct_member": "member", "reinstate": "member", "billing_manager": "", "hiring_manager": ""} {
		got, ok := organizationInvitationRole(in)
		if got != want || ok != (want != "") {
			t.Errorf("organizationInvitationRole(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestAccGithubOrganizationMembers(t *testing.T) {
	skipUnlessHasOrgs(t)
	skipUnlessHasOrgUser1(t)

	// Applying this resource removes every unlisted member of the test
	// organization, so the acceptance test only imports and reads it.
	t.Run("imports_current_members", func(t *testing.T) {
		config := fmt.Sprintf(`
resource "github_organization_members" "test" {
  members {
    username = "%s"
    role     = "member"
  }
}
`, testAccConf.testOrgUser1)

		resource.Test(t, resource.TestCase{
			ProviderFactories: providerFactories,
			Steps: []resource.TestStep{
				{
					Config:        config,
					ResourceName:  "github_organization_members.test",
					ImportState:   true,
					ImportStateId: testAccConf.owner,
					ImportStateCheck: func(states []*terraform.InstanceState) error {
						if len(states) != 1 {
							return fmt.Errorf("expected 1 state, got %d", len(states))
						}
						if states[0].Attributes["members.#"] == "0" {
							return fmt.Errorf("expected organization members, got none")
						}
						return nil
					},
				},
			},
		})
	})
}

func TestCallerAdminLockout(t *testing.T) {
	t.Parallel()

	admin := func(login string) userMember {
		return userMember{userIdentity: userIdentity{login: login}, role: "admin"}
	}
	member := func(login string) userMember {
		return userMember{userIdentity: userIdentity{login: login}, role: "member"}
	}

	tests := []struct {
		name    string
		current userMembers
		want    userMembers
		locked  bool
	}{
		{name: "caller_stays_admin", current: userMembers{admin("bot")}, want: userMembers{admin("Bot")}},
		{name: "caller_removed", current: userMembers{admin("bot"), admin("jane")}, want: userMembers{admin("jane")}, locked: true},
		{name: "caller_demoted", current: userMembers{admin("bot"), admin("jane")}, want: userMembers{admin("jane"), member("bot")}, locked: true},
		{name: "caller_not_an_admin", current: userMembers{member("bot"), admin("jane")}, want: userMembers{admin("jane")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := callerAdminLockout("bot", tt.current, tt.want)
			if (err != nil) != tt.locked {
				t.Errorf("error = %v, want locked = %v", err, tt.locked)
			}
		})
	}
}
