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

func resourceGithubOrganizationTeams() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceGithubOrganizationTeamsCreateOrUpdate,
		ReadContext:   resourceGithubOrganizationTeamsRead,
		UpdateContext: resourceGithubOrganizationTeamsCreateOrUpdate,
		DeleteContext: resourceGithubOrganizationTeamsDelete,
		Importer: &schema.ResourceImporter{
			StateContext: importOrganizationSingleton,
		},

		Description: "Resource to authoritatively manage which teams exist in a GitHub organization.",

		Schema: map[string]*schema.Schema{
			"team_slugs": {
				Type:     schema.TypeSet,
				Required: true,
				MinItems: 1,
				Elem: &schema.Schema{
					Type:             schema.TypeString,
					DiffSuppressFunc: caseInsensitive(),
				},
				Set:         caseInsensitiveStringHash,
				Description: "Slugs of the teams that may exist in the organization. Any other team is deleted.",
			},
		},
	}
}

func resourceGithubOrganizationTeamsCreateOrUpdate(ctx context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)
	if ok, diags := checkOrganizationOK(meta); !ok {
		return diags
	}

	slugs, _ := d.Get("team_slugs").(*schema.Set)
	want := expandStringList(slugs.List())

	current, err := getOrganizationTeamParents(ctx, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	deletions, err := organizationTeamDeletions(current, want)
	if err != nil {
		return diag.FromErr(err)
	}

	for _, slug := range deletions {
		tflog.Info(ctx, "Deleting team not in configuration.", map[string]any{"org": meta.name, "team_slug": slug})

		if _, err := meta.v3client.Teams.DeleteTeamBySlug(ctx, meta.name, slug); err != nil {
			// Deleting a parent team also deletes its child teams.
			if ghErr, ok := errors.AsType[*github.ErrorResponse](err); ok && ghErr.Response.StatusCode == http.StatusNotFound {
				continue
			}
			return diag.FromErr(fmt.Errorf("could not delete team %q: %w", slug, err))
		}
	}

	d.SetId(meta.name)

	return nil
}

func resourceGithubOrganizationTeamsRead(ctx context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)
	if ok, diags := checkOrganizationOK(meta); !ok {
		return diags
	}

	current, err := getOrganizationTeamParents(ctx, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	slugs := make([]any, 0, len(current))
	for slug := range current {
		slugs = append(slugs, slug)
	}

	if err := d.Set("team_slugs", slugs); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceGithubOrganizationTeamsDelete(ctx context.Context, _ *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)

	// Destroying this resource only stops enforcing the team list; deleting
	// every team would also drop all team repository access.
	summary := "Removing organization teams from state only; no teams were deleted."
	tflog.Warn(ctx, summary, map[string]any{"org": meta.name})

	return diag.Diagnostics{{Severity: diag.Warning, Summary: summary}}
}

// getOrganizationTeamParents returns every team slug in the organization,
// mapped to the slug of its parent team ("" for a top-level team).
func getOrganizationTeamParents(ctx context.Context, meta *Owner) (map[string]string, error) {
	teams := make(map[string]string)

	for team, err := range meta.v3client.Teams.ListTeamsIter(ctx, meta.name, &github.ListOptions{PerPage: meta.maxPerPage}) {
		if err != nil {
			return nil, fmt.Errorf("could not list organization teams: %w", err)
		}

		teams[strings.ToLower(team.GetSlug())] = strings.ToLower(team.GetParent().GetSlug())
	}

	tflog.Debug(ctx, "Got organization teams.", map[string]any{"org": meta.name, "teams": teams})

	return teams, nil
}

// organizationTeamDeletions returns the teams to delete, sorted by slug. It
// fails if a wanted team does not exist, or if a wanted team is nested under
// a team that would be deleted, because deleting a parent team also deletes
// its children.
func organizationTeamDeletions(current map[string]string, want []string) ([]string, error) {
	wanted := make(map[string]struct{}, len(want))
	for _, slug := range want {
		slug = strings.ToLower(slug)
		if _, ok := current[slug]; !ok {
			return nil, fmt.Errorf("team %q does not exist; create it, for example with github_team, before listing it", slug)
		}
		wanted[slug] = struct{}{}
	}

	var deletions []string
	for slug := range current {
		if _, ok := wanted[slug]; !ok {
			deletions = append(deletions, slug)
		}
	}
	slices.Sort(deletions)

	for slug := range wanted {
		seen := map[string]struct{}{slug: {}}
		for parent := current[slug]; parent != ""; parent = current[parent] {
			if _, ok := seen[parent]; ok {
				break
			}
			seen[parent] = struct{}{}

			if _, ok := wanted[parent]; !ok {
				return nil, fmt.Errorf("team %q is nested under team %q, which is not listed; deleting %q would also delete %q", slug, parent, parent, slug)
			}
		}
	}

	return deletions, nil
}
