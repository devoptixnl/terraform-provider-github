package github

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestOrganizationTeamDeletions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current map[string]string
		want    []string
		deleted []string
		err     string
	}{
		{
			name:    "no_changes",
			current: map[string]string{"maintainers": ""},
			want:    []string{"maintainers"},
		},
		{
			name:    "deletes_unlisted_teams",
			current: map[string]string{"maintainers": "", "zeta": "", "alpha": ""},
			want:    []string{"Maintainers"},
			deleted: []string{"alpha", "zeta"},
		},
		{
			name:    "deletes_unlisted_child_team",
			current: map[string]string{"parent": "", "child": "parent"},
			want:    []string{"parent"},
			deleted: []string{"child"},
		},
		{
			name:    "keeps_nested_chain",
			current: map[string]string{"a": "", "b": "a", "c": "b"},
			want:    []string{"a", "b", "c"},
		},
		{
			name:    "fails_on_missing_team",
			current: map[string]string{"maintainers": ""},
			want:    []string{"maintainers", "ghost"},
			err:     `team "ghost" does not exist`,
		},
		{
			name:    "fails_when_parent_would_be_deleted",
			current: map[string]string{"a": "", "b": "a", "c": "b"},
			want:    []string{"a", "c"},
			err:     `team "c" is nested under team "b"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			deleted, err := organizationTeamDeletions(tt.current, tt.want)

			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(deleted, tt.deleted) {
				t.Errorf("deleted = %v, want %v", deleted, tt.deleted)
			}
		})
	}
}

func TestAccGithubOrganizationTeams(t *testing.T) {
	skipUnlessHasOrgs(t)

	// Applying this resource deletes every unlisted team in the test
	// organization, so the acceptance test only imports and reads it.
	t.Run("imports_current_teams", func(t *testing.T) {
		team := mustCreateTestTeam(t)

		config := fmt.Sprintf(`
resource "github_organization_teams" "test" {
  team_slugs = ["%s"]
}
`, team.GetSlug())

		resource.Test(t, resource.TestCase{
			ProviderFactories: providerFactories,
			Steps: []resource.TestStep{
				{
					Config:        config,
					ResourceName:  "github_organization_teams.test",
					ImportState:   true,
					ImportStateId: testAccConf.owner,
					ImportStateCheck: func(states []*terraform.InstanceState) error {
						if len(states) != 1 {
							return fmt.Errorf("expected 1 state, got %d", len(states))
						}
						for k, v := range states[0].Attributes {
							if strings.HasPrefix(k, "team_slugs.") && v == strings.ToLower(team.GetSlug()) {
								return nil
							}
						}
						return fmt.Errorf("team %q not found in imported team_slugs", team.GetSlug())
					},
				},
			},
		})
	})
}
