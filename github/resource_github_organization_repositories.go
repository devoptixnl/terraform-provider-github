package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/go-github/v92/github"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceGithubOrganizationRepositories() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceGithubOrganizationRepositoriesCreateOrUpdate,
		ReadContext:   resourceGithubOrganizationRepositoriesRead,
		UpdateContext: resourceGithubOrganizationRepositoriesCreateOrUpdate,
		DeleteContext: stateOnlyDelete("Removing organization repositories from state only; no repositories were archived."),
		Importer: &schema.ResourceImporter{
			StateContext: importOrganizationSingleton,
		},
		CustomizeDiff: resourceGithubOrganizationRepositoriesDiff,

		Description: "Resource to authoritatively manage which repositories are active in a GitHub organization.",

		Schema: map[string]*schema.Schema{
			"repository_names": {
				Type:     schema.TypeSet,
				Required: true,
				MinItems: 1,
				Elem: &schema.Schema{
					Type:             schema.TypeString,
					DiffSuppressFunc: caseInsensitive(),
				},
				Set:         caseInsensitiveStringHash,
				Description: "Names of the repositories that may stay active in the organization. Any other repository is archived.",
			},
			"repositories_to_archive": {
				Type:        schema.TypeSet,
				Computed:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Active repositories that are not listed and will be archived on the next apply. Shown in the plan; empty after apply.",
			},
		},
	}
}

// resourceGithubOrganizationRepositoriesDiff puts the repositories that the
// apply will archive into the plan, so a reviewer sees them before they are
// archived, including on the first apply when there is no prior state.
func resourceGithubOrganizationRepositoriesDiff(ctx context.Context, d *schema.ResourceDiff, m any) error {
	meta, _ := m.(*Owner)
	if !meta.IsOrganization {
		return nil
	}

	if !d.NewValueKnown("repository_names") {
		return d.SetNewComputed("repositories_to_archive")
	}

	current, err := getOrganizationRepositories(ctx, meta)
	if err != nil {
		return err
	}

	names, _ := d.Get("repository_names").(*schema.Set)
	archivals, _ := organizationRepositoryArchivals(current, expandStringList(names.List()))

	pending := make([]any, 0, len(archivals))
	for _, repo := range archivals {
		pending = append(pending, repo.name)
	}

	old, _ := d.GetChange("repositories_to_archive")
	if oldSet, ok := old.(*schema.Set); ok && oldSet.Len() == 0 && len(pending) == 0 {
		return nil
	}

	return d.SetNew("repositories_to_archive", pending)
}

func resourceGithubOrganizationRepositoriesCreateOrUpdate(ctx context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)
	if ok, diags := checkOrganizationOK(meta); !ok {
		return diags
	}

	names, _ := d.Get("repository_names").(*schema.Set)
	want := expandStringList(names.List())

	current, err := getOrganizationRepositories(ctx, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	archivals, missing := organizationRepositoryArchivals(current, want)
	// The organization listing can lag behind a repository created earlier in
	// the same apply, so confirm each missing name directly.
	for _, name := range missing {
		repo, _, err := meta.v3client.Repositories.Get(ctx, meta.name, name)
		if err != nil {
			if ghErr, ok := errors.AsType[*github.ErrorResponse](err); ok && ghErr.Response.StatusCode == http.StatusNotFound {
				return diag.Errorf("repository %q does not exist; create it, for example with github_repository, before listing it", name)
			}
			return diag.FromErr(fmt.Errorf("could not get repository %q: %w", name, err))
		}
		tflog.Debug(ctx, "Listed repository found outside the organization listing.", map[string]any{"org": meta.name, "repository": repo.GetName()})
	}

	for _, repo := range archivals {
		tflog.Info(ctx, "Archiving repository not in configuration.", map[string]any{"org": meta.name, "repository": repo.name})

		if _, _, err := meta.v3client.Repositories.Edit(ctx, meta.name, repo.name, &github.Repository{Archived: new(true)}); err != nil {
			return diag.FromErr(fmt.Errorf("could not archive repository %q: %w", repo.name, err))
		}
	}

	d.SetId(meta.name)
	if err := d.Set("repositories_to_archive", []any{}); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceGithubOrganizationRepositoriesRead(ctx context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)
	if ok, diags := checkOrganizationOK(meta); !ok {
		return diags
	}

	current, err := getOrganizationRepositories(ctx, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	names, _ := d.Get("repository_names").(*schema.Set)
	var configured []string
	if names != nil {
		configured = expandStringList(names.List())
	}

	if err := d.Set("repository_names", organizationRepositoryNamesState(current, configured)); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

type organizationRepository struct {
	name     string
	archived bool
}

// getOrganizationRepositories returns every repository in the organization
// that the provider's credential can see, keyed by lowercased name.
func getOrganizationRepositories(ctx context.Context, meta *Owner) (map[string]organizationRepository, error) {
	repos := make(map[string]organizationRepository)

	opts := &github.RepositoryListByOrgOptions{Type: "all", ListOptions: github.ListOptions{PerPage: meta.maxPerPage}}
	for repo, err := range meta.v3client.Repositories.ListByOrgIter(ctx, meta.name, opts) {
		if err != nil {
			return nil, fmt.Errorf("could not list organization repositories: %w", err)
		}

		repos[strings.ToLower(repo.GetName())] = organizationRepository{name: repo.GetName(), archived: repo.GetArchived()}
	}

	tflog.Debug(ctx, "Got organization repositories.", map[string]any{"org": meta.name, "count": len(repos)})

	return repos, nil
}

// organizationRepositoryArchivals returns the active repositories that are
// not listed, sorted by name, and the listed names that are not in current.
func organizationRepositoryArchivals(current map[string]organizationRepository, want []string) (archivals []organizationRepository, missing []string) {
	wanted := make(map[string]struct{}, len(want))
	for _, name := range want {
		key := strings.ToLower(name)
		if _, ok := current[key]; !ok {
			missing = append(missing, name)
		}
		wanted[key] = struct{}{}
	}
	slices.Sort(missing)

	for key, repo := range current {
		if _, ok := wanted[key]; !ok && !repo.archived {
			archivals = append(archivals, repo)
		}
	}
	slices.SortFunc(archivals, func(a, b organizationRepository) int {
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})

	return archivals, missing
}

// organizationRepositoryNamesState returns the names to store in state: every
// active repository plus every configured repository that still exists,
// archived or not. An unlisted active repository or a deleted listed one
// therefore shows up as drift, while archived repositories that are not
// listed stay out of the diff.
func organizationRepositoryNamesState(current map[string]organizationRepository, configured []string) []any {
	keep := make(map[string]string, len(current))
	for key, repo := range current {
		if !repo.archived {
			keep[key] = repo.name
		}
	}
	for _, name := range configured {
		key := strings.ToLower(name)
		if repo, ok := current[key]; ok {
			keep[key] = repo.name
		}
	}

	names := make([]any, 0, len(keep))
	for _, name := range keep {
		names = append(names, name)
	}

	return names
}
