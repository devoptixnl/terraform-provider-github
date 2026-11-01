package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	sdkterraform "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
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
		missing  []string
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
			name:    "reports_missing_repository",
			current: map[string]organizationRepository{"website": {name: "website"}},
			want:    []string{"website", "Ghost"},
			missing: []string{"Ghost"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			archivals, missing := organizationRepositoryArchivals(tt.current, tt.want)

			if !slices.Equal(missing, tt.missing) {
				t.Errorf("missing = %v, want %v", missing, tt.missing)
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
		name, _ := v.(string)
		got = append(got, name)
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

func TestOrganizationRepositoriesCreateOrUpdate(t *testing.T) {
	t.Parallel()

	newServer := func(t *testing.T, archived *[]string) *Owner {
		t.Helper()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /orgs/acme/repos", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `[{"name":"website"},{"name":"Stale"},{"name":"frozen","archived":true}]`)
		})
		mux.HandleFunc("GET /repos/acme/lagging", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"name":"lagging"}`)
		})
		mux.HandleFunc("GET /repos/acme/ghost", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
		})
		mux.HandleFunc("PATCH /repos/acme/{repo}", func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["archived"] != true || len(body) != 1 {
				t.Errorf("PATCH %s body = %v, want only archived=true", r.URL.Path, body)
			}
			*archived = append(*archived, r.PathValue("repo"))
			_, _ = io.WriteString(w, `{}`)
		})

		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)

		return &Owner{name: "acme", IsOrganization: true, maxPerPage: 100, v3client: mustCreateTestGitHubClient(t, server.URL+"/")}
	}

	t.Run("plan_lists_repositories_to_archive", func(t *testing.T) {
		t.Parallel()

		var archived []string
		meta := newServer(t, &archived)
		config := sdkterraform.NewResourceConfigRaw(map[string]any{"repository_names": []any{"website"}})

		diff, err := resourceGithubOrganizationRepositories().Diff(t.Context(), nil, config, meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var pending []string
		for k, attr := range diff.Attributes {
			if strings.HasPrefix(k, "repositories_to_archive.") && k != "repositories_to_archive.#" {
				pending = append(pending, attr.New)
			}
		}
		if !slices.Equal(pending, []string{"Stale"}) {
			t.Errorf("repositories_to_archive = %v, want [Stale]", pending)
		}
		if len(archived) != 0 {
			t.Errorf("plan archived %v", archived)
		}
	})

	t.Run("archives_only_unlisted_active_repositories", func(t *testing.T) {
		t.Parallel()

		var archived []string
		meta := newServer(t, &archived)
		d := schema.TestResourceDataRaw(t, resourceGithubOrganizationRepositories().Schema, map[string]any{
			"repository_names": []any{"Website", "lagging"},
		})

		if diags := resourceGithubOrganizationRepositoriesCreateOrUpdate(t.Context(), d, meta); diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if !slices.Equal(archived, []string{"Stale"}) {
			t.Errorf("archived = %v, want [Stale]", archived)
		}
		if d.Id() != "acme" {
			t.Errorf("id = %q, want acme", d.Id())
		}
	})

	t.Run("fails_before_archiving_when_listed_repository_is_missing", func(t *testing.T) {
		t.Parallel()

		var archived []string
		meta := newServer(t, &archived)
		d := schema.TestResourceDataRaw(t, resourceGithubOrganizationRepositories().Schema, map[string]any{
			"repository_names": []any{"website", "ghost"},
		})

		diags := resourceGithubOrganizationRepositoriesCreateOrUpdate(t.Context(), d, meta)
		if !diags.HasError() || !strings.Contains(diags[0].Summary, `repository "ghost" does not exist`) {
			t.Fatalf("diags = %v, want a missing repository error", diags)
		}
		if len(archived) != 0 {
			t.Errorf("archived = %v, want none", archived)
		}
		if d.Id() != "" {
			t.Errorf("id = %q, want it unset", d.Id())
		}
	})
}
