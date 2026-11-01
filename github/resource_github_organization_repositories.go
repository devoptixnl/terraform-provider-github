package github

import (
	"context"
	"fmt"
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
		DeleteContext: resourceGithubOrganizationRepositoriesDelete,
		Importer: &schema.ResourceImporter{
			StateContext: resourceGithubOrganizationRepositoriesImport,
		},

		Description: "Resource to authoritatively manage which repositories are active in a GitHub organization.",

		Schema: map[string]*schema.Schema{
			"repository_names": {
				Type:     schema.TypeSet,
				Required: true,
				Elem: &schema.Schema{
					Type:             schema.TypeString,
					DiffSuppressFunc: caseInsensitive(),
				},
				Set:         func(v any) int { return schema.HashString(strings.ToLower(v.(string))) },
				Description: "Names of the repositories that may stay active in the organization. Any other repository is archived.",
			},
		},
	}
}

func resourceGithubOrganizationRepositoriesCreateOrUpdate(ctx context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)
	if ok, diags := checkOrganizationOK(meta); !ok {
		return diags
	}

	names, _ := d.Get("repository_names").(*schema.Set)
	want := make([]string, 0, names.Len())
	for _, v := range names.List() {
		want = append(want, v.(string))
	}

	current, err := getOrganizationRepositories(ctx, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	archivals, err := organizationRepositoryArchivals(current, want)
	if err != nil {
		return diag.FromErr(err)
	}

	for _, repo := range archivals {
		tflog.Info(ctx, "Archiving repository not in configuration.", map[string]any{"org": meta.name, "repository": repo.name})

		if _, _, err := meta.v3client.Repositories.Edit(ctx, meta.name, repo.name, &github.Repository{Archived: new(true)}); err != nil {
			return diag.FromErr(fmt.Errorf("could not archive repository %q: %w", repo.name, err))
		}
	}

	d.SetId(meta.name)

	return resourceGithubOrganizationRepositoriesRead(ctx, d, m)
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

	var configured []string
	if names, ok := d.Get("repository_names").(*schema.Set); ok {
		for _, v := range names.List() {
			configured = append(configured, v.(string))
		}
	}

	if err := d.Set("repository_names", organizationRepositoryNamesState(current, configured)); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceGithubOrganizationRepositoriesDelete(ctx context.Context, _ *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)

	// Destroying this resource only stops enforcing the repository list;
	// archiving every repository would freeze the whole organization.
	tflog.Warn(ctx, "Removing organization repositories from state only; no repositories were archived.", map[string]any{"org": meta.name})

	return nil
}

func resourceGithubOrganizationRepositoriesImport(ctx context.Context, d *schema.ResourceData, m any) ([]*schema.ResourceData, error) {
	meta, _ := m.(*Owner)
	if err := checkOrganization(meta); err != nil {
		return nil, err
	}

	if id := d.Id(); !strings.EqualFold(id, meta.name) {
		return nil, fmt.Errorf("import ID %q must be the provider's organization %q", id, meta.name)
	}

	d.SetId(meta.name)

	return []*schema.ResourceData{d}, nil
}

type organizationRepository struct {
	name     string
	archived bool
}

// getOrganizationRepositories returns every repository in the organization,
// keyed by lowercased name.
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
// not listed, sorted by name. It fails if a listed repository does not exist.
func organizationRepositoryArchivals(current map[string]organizationRepository, want []string) ([]organizationRepository, error) {
	wanted := make(map[string]struct{}, len(want))
	for _, name := range want {
		name = strings.ToLower(name)
		if _, ok := current[name]; !ok {
			return nil, fmt.Errorf("repository %q does not exist; create it, for example with github_repository, before listing it", name)
		}
		wanted[name] = struct{}{}
	}

	var archivals []organizationRepository
	for key, repo := range current {
		if _, ok := wanted[key]; !ok && !repo.archived {
			archivals = append(archivals, repo)
		}
	}
	slices.SortFunc(archivals, func(a, b organizationRepository) int {
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})

	return archivals, nil
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
