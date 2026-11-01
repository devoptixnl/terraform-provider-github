package github

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestOrganizationRepositoryArchivals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		current  map[string]organizationRepository
		want     []string
		archived []string
		err      string
	}{
		{
			name:    "no_changes",
			current: map[string]organizationRepository{"website": {name: "website"}},
			want:    []string{"website"},
		},
		{
			name: "archives_unlisted_active_repositories",
			current: map[string]organizationRepository{
				"website": {name: "website"},
				"zeta":    {name: "Zeta"},
				"alpha":   {name: "alpha"},
			},
			want:     []string{"Website"},
			archived: []string{"alpha", "Zeta"},
		},
		{
			name: "skips_already_archived_repositories",
			current: map[string]organizationRepository{
				"website": {name: "website"},
				"old":     {name: "old", archived: true},
			},
			want: []string{"website"},
		},
		{
			name:    "keeps_listed_archived_repository",
			current: map[string]organizationRepository{"old": {name: "old", archived: true}},
			want:    []string{"old"},
		},
		{
			name:    "fails_on_missing_repository",
			current: map[string]organizationRepository{"website": {name: "website"}},
			want:    []string{"website", "ghost"},
			err:     `repository "ghost" does not exist`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			archivals, err := organizationRepositoryArchivals(tt.current, tt.want)

			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var names []string
			for _, repo := range archivals {
				names = append(names, repo.name)
			}
			if !slices.Equal(names, tt.archived) {
				t.Errorf("archived = %v, want %v", names, tt.archived)
			}
		})
	}
}

func TestOrganizationRepositoryNamesState(t *testing.T) {
	t.Parallel()

	current := map[string]organizationRepository{
		"website":       {name: "Website"},
		"unlisted":      {name: "unlisted"},
		"old":           {name: "old", archived: true},
		"listed-frozen": {name: "listed-frozen", archived: true},
	}
	configured := []string{"website", "listed-frozen", "deleted"}

	got := make([]string, 0)
	for _, v := range organizationRepositoryNamesState(current, configured) {
		got = append(got, v.(string))
	}
	slices.Sort(got)

	want := []string{"Website", "listed-frozen", "unlisted"}
	if !slices.Equal(got, want) {
		t.Errorf("state = %v, want %v", got, want)
	}
}

func TestAccGithubOrganizationRepositories(t *testing.T) {
	skipUnlessHasOrgs(t)

	// Applying this resource archives every unlisted repository in the test
	// organization, so the acceptance test only imports and reads it.
	t.Run("imports_current_repositories", func(t *testing.T) {
		repo := mustCreateTestRepository(t)

		config := fmt.Sprintf(`
resource "github_organization_repositories" "test" {
  repository_names = ["%s"]
}
`, repo.GetName())

		resource.Test(t, resource.TestCase{
			ProviderFactories: providerFactories,
			Steps: []resource.TestStep{
				{
					Config:        config,
					ResourceName:  "github_organization_repositories.test",
					ImportState:   true,
					ImportStateId: testAccConf.owner,
					ImportStateCheck: func(states []*terraform.InstanceState) error {
						if len(states) != 1 {
							return fmt.Errorf("expected 1 state, got %d", len(states))
						}
						for k, v := range states[0].Attributes {
							if strings.HasPrefix(k, "repository_names.") && strings.EqualFold(v, repo.GetName()) {
								return nil
							}
						}
						return fmt.Errorf("repository %q not found in imported repository_names", repo.GetName())
					},
				},
			},
		})
	})
}
