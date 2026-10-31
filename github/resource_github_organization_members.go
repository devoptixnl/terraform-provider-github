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

func resourceGithubOrganizationMembers() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceGithubOrganizationMembersCreateOrUpdate,
		ReadContext:   resourceGithubOrganizationMembersRead,
		UpdateContext: resourceGithubOrganizationMembersCreateOrUpdate,
		DeleteContext: resourceGithubOrganizationMembersDelete,
		Importer: &schema.ResourceImporter{
			StateContext: resourceGithubOrganizationMembersImport,
		},

		CustomizeDiff: resourceGithubOrganizationMembersDiff,

		Description: "Resource to authoritatively manage the members of a GitHub organization.",

		Schema: map[string]*schema.Schema{
			"members": {
				Type:        schema.TypeSet,
				Required:    true,
				MinItems:    1,
				Description: "Users that should be members of the organization. Any other member is removed and any other pending invitation is cancelled.",
				// Set identity ignores username case so a case-only config change is a no-op;
				// duplicates are rejected by diffDuplicateUsernameCheck.
				Set: func(v any) int {
					username, _ := v.(map[string]any)["username"].(string)
					role, _ := v.(map[string]any)["role"].(string)
					return schema.HashString("username:" + strings.ToLower(username) + ";role:" + strings.ToLower(role))
				},
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"username": {
							Type:             schema.TypeString,
							Required:         true,
							DiffSuppressFunc: caseInsensitive(),
							Description:      "User to keep in, or invite to, the organization.",
						},
						"role": {
							Type:             schema.TypeString,
							Optional:         true,
							Default:          "member",
							Description:      "Role of the user in the organization; must be one of `member` or `admin`.",
							ValidateDiagFunc: validateValueFunc([]string{"member", "admin"}),
						},
					},
				},
			},
		},
	}
}

func resourceGithubOrganizationMembersDiff(ctx context.Context, d *schema.ResourceDiff, _ any) error {
	if err := diffDuplicateUsernameCheck(ctx, d, "members"); err != nil {
		return fmt.Errorf("error diffing members config: %w", err)
	}

	if !d.NewValueKnown("members") {
		return nil
	}

	members, _ := d.Get("members").(*schema.Set)
	for _, v := range members.List() {
		if role, _ := v.(map[string]any)["role"].(string); role == "admin" {
			return nil
		}
	}

	return errors.New("members must contain at least one user with the `admin` role, otherwise the organization would lose all of its owners")
}

func resourceGithubOrganizationMembersCreateOrUpdate(ctx context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)
	if ok, diags := checkOrganizationOK(meta); !ok {
		return diags
	}

	members, _ := d.Get("members").(*schema.Set)
	want, err := newUserMembers(members.List())
	if err != nil {
		return diag.FromErr(fmt.Errorf("failed to parse organization members: %w", err))
	}

	current, err := getOrganizationMembers(ctx, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	upserts, removals := organizationMemberChanges(current, want)

	// Add and promote first so the organization never drops below the
	// owners in the configuration while removals run.
	for _, member := range upserts {
		tflog.Info(ctx, "Setting organization membership.", map[string]any{"org": meta.name, "username": member.login, "role": member.role})

		if _, _, err := meta.v3client.Organizations.EditOrgMembership(ctx, member.login, meta.name, &github.Membership{Role: new(member.role)}); err != nil {
			return diag.FromErr(fmt.Errorf("could not set organization membership for %q: %w", member.login, err))
		}
	}

	// Removing a membership also cancels a pending invitation for that user.
	for _, login := range removals {
		tflog.Info(ctx, "Removing organization membership.", map[string]any{"org": meta.name, "username": login})

		if _, err := meta.v3client.Organizations.RemoveOrgMembership(ctx, login, meta.name); err != nil {
			if ghErr, ok := errors.AsType[*github.ErrorResponse](err); ok && ghErr.Response.StatusCode == http.StatusNotFound {
				continue
			}
			return diag.FromErr(fmt.Errorf("could not remove organization member %q: %w", login, err))
		}
	}

	d.SetId(meta.name)

	return resourceGithubOrganizationMembersRead(ctx, d, m)
}

func resourceGithubOrganizationMembersRead(ctx context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)
	if ok, diags := checkOrganizationOK(meta); !ok {
		return diags
	}

	current, err := getOrganizationMembers(ctx, meta)
	if err != nil {
		return diag.FromErr(err)
	}

	if err := d.Set("members", current.flatten()); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func resourceGithubOrganizationMembersDelete(ctx context.Context, _ *schema.ResourceData, m any) diag.Diagnostics {
	meta, _ := m.(*Owner)

	// Removing every member would lock everyone, including the caller, out of
	// the organization, so destroying this resource only stops managing them.
	tflog.Warn(ctx, "Removing organization members from state only; no memberships were changed.", map[string]any{"org": meta.name})

	return nil
}

func resourceGithubOrganizationMembersImport(ctx context.Context, d *schema.ResourceData, m any) ([]*schema.ResourceData, error) {
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

// getOrganizationMembers returns every member of the organization plus every
// pending invitation for a GitHub user, each with its organization role.
// Pending invitations count as members so that inviting a user does not show
// as drift until they accept.
func getOrganizationMembers(ctx context.Context, meta *Owner) (userMembers, error) {
	client := meta.v3client

	var members userMembers
	seen := make(map[string]struct{})

	for _, role := range []string{"admin", "member"} {
		opts := &github.ListMembersOptions{Role: role, ListOptions: github.ListOptions{PerPage: meta.maxPerPage}}
		for user, err := range client.Organizations.ListMembersIter(ctx, meta.name, opts) {
			if err != nil {
				return nil, fmt.Errorf("could not list organization members with role %q: %w", role, err)
			}

			login := strings.ToLower(user.GetLogin())
			if _, ok := seen[login]; ok {
				continue
			}
			seen[login] = struct{}{}
			members = append(members, userMember{userIdentity: userIdentity{login: login}, role: role})
		}
	}

	for invitation, err := range client.Organizations.ListPendingOrgInvitationsIter(ctx, meta.name, &github.ListOptions{PerPage: meta.maxPerPage}) {
		if err != nil {
			return nil, fmt.Errorf("could not list pending organization invitations: %w", err)
		}

		login := strings.ToLower(invitation.GetLogin())
		if login == "" {
			// Invitations by email have no user yet and cannot be matched to config.
			continue
		}

		role, ok := organizationInvitationRole(invitation.GetRole())
		if !ok {
			continue
		}

		if _, ok := seen[login]; ok {
			continue
		}
		seen[login] = struct{}{}
		members = append(members, userMember{userIdentity: userIdentity{login: login}, role: role})
	}

	tflog.Debug(ctx, "Got organization members.", map[string]any{"org": meta.name, "members": members.flatten()})

	return members, nil
}

// organizationInvitationRole maps an invitation role to an organization
// membership role. Roles that do not make the user a member are skipped.
func organizationInvitationRole(role string) (string, bool) {
	switch role {
	case "admin":
		return "admin", true
	case "direct_member", "reinstate":
		return "member", true
	default:
		return "", false
	}
}

// organizationMemberChanges compares the current and wanted members. It
// returns the members to add or change role for, and the logins to remove,
// both sorted by login.
func organizationMemberChanges(current, want userMembers) (userMembers, []string) {
	currentRoles := make(map[string]string, len(current))
	for _, member := range current {
		currentRoles[strings.ToLower(member.login)] = member.role
	}

	wantLogins := make(map[string]struct{}, len(want))
	var upserts userMembers
	for _, member := range want {
		login := strings.ToLower(member.login)
		wantLogins[login] = struct{}{}

		if role, ok := currentRoles[login]; !ok || role != member.role {
			upserts = append(upserts, userMember{userIdentity: userIdentity{login: login}, role: member.role})
		}
	}

	var removals []string
	for login := range currentRoles {
		if _, ok := wantLogins[login]; !ok {
			removals = append(removals, login)
		}
	}

	slices.SortFunc(upserts, func(a, b userMember) int { return strings.Compare(a.login, b.login) })
	slices.Sort(removals)

	return upserts, removals
}
