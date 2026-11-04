package github

import (
	"context"
	"errors"
	"log"
	"strconv"

	"github.com/google/go-github/v92/github"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceGithubOrganizationSettings() *schema.Resource {
	return &schema.Resource{
		Create: resourceGithubOrganizationSettingsCreateOrUpdate,
		Read:   resourceGithubOrganizationSettingsRead,
		Update: resourceGithubOrganizationSettingsCreateOrUpdate,
		Delete: resourceGithubOrganizationSettingsDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: map[string]*schema.Schema{
			"billing_email": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The billing email address for the organization.",
			},
			"company": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The company name for the organization.",
			},
			"email": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The email address for the organization.",
			},
			"twitter_username": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The Twitter username for the organization.",
			},
			"location": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The location for the organization.",
			},
			"name": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The name for the organization.",
			},
			"description": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The description for the organization.",
			},
			"has_organization_projects": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not organization projects are enabled for the organization.",
			},
			"has_repository_projects": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not repository projects are enabled for the organization.",
			},
			"default_repository_permission": {
				Type:             schema.TypeString,
				Optional:         true,
				Default:          "read",
				Description:      "The default permission for organization members to create new repositories. Can be one of 'read', 'write', 'admin' or 'none'.",
				ValidateDiagFunc: validation.ToDiagFunc(validation.StringInSlice([]string{"read", "write", "admin", "none"}, false)),
			},
			"members_can_create_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not organization members can create new repositories.",
			},
			"members_can_create_internal_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Whether or not organization members can create new internal repositories. For Enterprise Organizations only.",
			},
			"members_can_create_private_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not organization members can create new private repositories.",
			},
			"members_can_create_public_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not organization members can create new public repositories.",
			},
			"members_can_create_pages": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not organization members can create new pages.",
			},
			"members_can_create_public_pages": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not organization members can create new public pages.",
			},
			"members_can_create_private_pages": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not organization members can create new private pages.",
			},
			"members_can_fork_private_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Whether or not organization members can fork private repositories.",
			},
			"web_commit_signoff_required": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Whether or not commit signatures are required for commits to the organization.",
			},
			"blog": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The blog URL for the organization.",
			},
			"advanced_security_enabled_for_new_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: " Whether or not advanced security is enabled for new repositories.",
			},
			"dependabot_alerts_enabled_for_new_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Whether or not dependabot alerts are enabled for new repositories.",
			},
			"dependabot_security_updates_enabled_for_new_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: " Whether or not dependabot security updates are enabled for new repositories.",
			},
			"dependency_graph_enabled_for_new_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Whether or not dependency graph is enabled for new repositories.",
			},
			"secret_scanning_enabled_for_new_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Whether or not secret scanning is enabled for new repositories.",
			},
			"secret_scanning_push_protection_enabled_for_new_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Whether or not secret scanning push protection is enabled for new repositories.",
			},
			"secret_scanning_push_protection_custom_link_enabled": {
				Type:        schema.TypeBool,
				Optional:    true,
				Computed:    true,
				Description: "Whether a custom link is shown to contributors who are blocked from pushing a secret by push protection.",
			},
			"secret_scanning_push_protection_custom_link": {
				Type:             schema.TypeString,
				Optional:         true,
				Computed:         true,
				ValidateDiagFunc: validation.ToDiagFunc(validation.IsURLWithHTTPorHTTPS),
				Description:      "The URL shown to contributors who are blocked from pushing a secret by push protection, when `secret_scanning_push_protection_custom_link_enabled` is true.",
			},
			"deploy_keys_enabled_for_repositories": {
				Type:        schema.TypeBool,
				Optional:    true,
				Computed:    true,
				Description: "Whether deploy keys may be added and used for repositories in the organization.",
			},
		},
	}
}

// buildOrganizationSettings creates the github.Organization payload. On create it includes every field that is
// configured or has a schema default, matching what an update converges to. On update it only includes fields
// that have changed, to avoid API validation errors. Optional+Computed fields left unset are never sent.
func buildOrganizationSettings(d *schema.ResourceData, isEnterprise bool) *github.Organization {
	settings := &github.Organization{}

	// Check if this is an update (has ID) or create (no ID)
	isUpdate := d.Id() != ""

	// Helper function to check if field should be included
	shouldInclude := func(fieldName string) bool {
		if !isUpdate {
			_, ok := d.GetOkExists(fieldName) //nolint:staticcheck // SA1019: GetOk cannot tell an explicit false from unset.
			return ok
		}
		// For updates, only include if the field has changed
		return d.HasChange(fieldName)
	}

	// Required field - always include if configured (API requires it even if unchanged)
	if billingEmail, ok := d.GetOk("billing_email"); ok {
		settings.BillingEmail = new(billingEmail.(string))
	}

	// Optional string fields - only set if should be included
	if shouldInclude("company") {
		if company, ok := d.GetOk("company"); ok {
			settings.Company = new(company.(string))
		}
	}
	if shouldInclude("email") {
		if email, ok := d.GetOk("email"); ok {
			settings.Email = new(email.(string))
		}
	}
	if shouldInclude("twitter_username") {
		if twitterUsername, ok := d.GetOk("twitter_username"); ok {
			settings.TwitterUsername = new(twitterUsername.(string))
		}
	}
	if shouldInclude("location") {
		if location, ok := d.GetOk("location"); ok {
			settings.Location = new(location.(string))
		}
	}
	if shouldInclude("name") {
		if name, ok := d.GetOk("name"); ok {
			settings.Name = new(name.(string))
		}
	}
	if shouldInclude("description") {
		if description, ok := d.GetOk("description"); ok {
			settings.Description = new(description.(string))
		}
	}
	if shouldInclude("blog") {
		if blog, ok := d.GetOk("blog"); ok {
			settings.Blog = new(blog.(string))
		}
	}

	// Boolean fields - only set if should be included
	// Use d.Get() instead of d.GetOk() when shouldInclude() returns true,
	// because we already know the field should be included, and d.Get() correctly handles false values
	if shouldInclude("has_organization_projects") {
		settings.HasOrganizationProjects = new(d.Get("has_organization_projects").(bool))
	}
	if shouldInclude("has_repository_projects") {
		settings.HasRepositoryProjects = new(d.Get("has_repository_projects").(bool))
	}
	if shouldInclude("default_repository_permission") {
		if defaultRepoPermission, ok := d.GetOk("default_repository_permission"); ok {
			settings.DefaultRepoPermission = new(defaultRepoPermission.(string))
		}
	}
	if shouldInclude("members_can_create_repositories") {
		settings.MembersCanCreateRepos = new(d.Get("members_can_create_repositories").(bool))
	}
	if shouldInclude("members_can_create_private_repositories") {
		settings.MembersCanCreatePrivateRepos = new(d.Get("members_can_create_private_repositories").(bool))
	}
	if shouldInclude("members_can_create_public_repositories") {
		settings.MembersCanCreatePublicRepos = new(d.Get("members_can_create_public_repositories").(bool))
	}
	if shouldInclude("members_can_create_pages") {
		settings.MembersCanCreatePages = new(d.Get("members_can_create_pages").(bool))
	}
	if shouldInclude("members_can_create_public_pages") {
		settings.MembersCanCreatePublicPages = new(d.Get("members_can_create_public_pages").(bool))
	}
	if shouldInclude("members_can_create_private_pages") {
		settings.MembersCanCreatePrivatePages = new(d.Get("members_can_create_private_pages").(bool))
	}
	if shouldInclude("members_can_fork_private_repositories") {
		settings.MembersCanForkPrivateRepos = new(d.Get("members_can_fork_private_repositories").(bool))
	}
	if shouldInclude("web_commit_signoff_required") {
		settings.WebCommitSignoffRequired = new(d.Get("web_commit_signoff_required").(bool))
	}
	if shouldInclude("advanced_security_enabled_for_new_repositories") {
		settings.AdvancedSecurityEnabledForNewRepos = new(d.Get("advanced_security_enabled_for_new_repositories").(bool))
	}
	if shouldInclude("dependabot_alerts_enabled_for_new_repositories") {
		settings.DependabotAlertsEnabledForNewRepos = new(d.Get("dependabot_alerts_enabled_for_new_repositories").(bool))
	}
	if shouldInclude("dependabot_security_updates_enabled_for_new_repositories") {
		settings.DependabotSecurityUpdatesEnabledForNewRepos = new(d.Get("dependabot_security_updates_enabled_for_new_repositories").(bool))
	}
	if shouldInclude("dependency_graph_enabled_for_new_repositories") {
		settings.DependencyGraphEnabledForNewRepos = new(d.Get("dependency_graph_enabled_for_new_repositories").(bool))
	}
	if shouldInclude("secret_scanning_enabled_for_new_repositories") {
		settings.SecretScanningEnabledForNewRepos = new(d.Get("secret_scanning_enabled_for_new_repositories").(bool))
	}
	if shouldInclude("secret_scanning_push_protection_enabled_for_new_repositories") {
		settings.SecretScanningPushProtectionEnabledForNewRepos = new(d.Get("secret_scanning_push_protection_enabled_for_new_repositories").(bool))
	}
	if v, ok := d.Get("secret_scanning_push_protection_custom_link_enabled").(bool); ok && shouldInclude("secret_scanning_push_protection_custom_link_enabled") {
		settings.SecretScanningPushProtectionCustomLinkEnabled = new(v)
	}
	if v, ok := d.Get("secret_scanning_push_protection_custom_link").(string); ok && shouldInclude("secret_scanning_push_protection_custom_link") {
		settings.SecretScanningPushProtectionCustomLink = new(v)
	}
	if v, ok := d.Get("deploy_keys_enabled_for_repositories").(bool); ok && shouldInclude("deploy_keys_enabled_for_repositories") {
		settings.DeployKeysEnabledForRepositories = new(v)
	}

	// Enterprise-specific field
	if isEnterprise {
		if shouldInclude("members_can_create_internal_repositories") {
			settings.MembersCanCreateInternalRepos = new(d.Get("members_can_create_internal_repositories").(bool))
		}
	}

	return settings
}

func resourceGithubOrganizationSettingsCreateOrUpdate(d *schema.ResourceData, meta any) error {
	err := checkOrganization(meta)
	if err != nil {
		return err
	}
	client := meta.(*Owner).v3client
	ctx := context.WithValue(context.Background(), ctxId, d.Id())
	org := meta.(*Owner).name

	orgInfo, _, err := client.Organizations.Get(ctx, org)
	if err != nil {
		return err
	}

	// Build settings using helper function
	isEnterprise := orgInfo.GetPlan().GetName() == "enterprise"
	settings := buildOrganizationSettings(d, isEnterprise)

	log.Printf("[DEBUG] Built settings for org %s (enterprise: %v): %s", org, isEnterprise, settings)

	orgSettings, _, err := client.Organizations.Edit(ctx, org, settings)
	if err != nil {
		// Log detailed error information for debugging
		if ghErr, ok := errors.AsType[*github.ErrorResponse](err); ok {
			log.Printf("[DEBUG] GitHub API Error: Status=%d, Message=%s", ghErr.Response.StatusCode, ghErr.Message)
			if len(ghErr.Errors) > 0 {
				for i, apiErr := range ghErr.Errors {
					log.Printf("[DEBUG]   Error[%d]: Resource=%s, Field=%s, Code=%s, Message=%s",
						i, apiErr.Resource, apiErr.Field, apiErr.Code, apiErr.Message)
				}
			}
		}
		return err
	}
	id := strconv.FormatInt(orgSettings.GetID(), 10)
	d.SetId(id)

	return resourceGithubOrganizationSettingsRead(d, meta)
}

func resourceGithubOrganizationSettingsRead(d *schema.ResourceData, meta any) error {
	err := checkOrganization(meta)
	if err != nil {
		return err
	}
	client := meta.(*Owner).v3client
	ctx := context.Background()
	org := meta.(*Owner).name

	orgSettings, _, err := client.Organizations.Get(ctx, org)
	if err != nil {
		return err
	}

	if err = d.Set("billing_email", orgSettings.GetBillingEmail()); err != nil {
		return err
	}
	if err = d.Set("company", orgSettings.GetCompany()); err != nil {
		return err
	}
	if err = d.Set("email", orgSettings.GetEmail()); err != nil {
		return err
	}
	if err = d.Set("twitter_username", orgSettings.GetTwitterUsername()); err != nil {
		return err
	}
	if err = d.Set("location", orgSettings.GetLocation()); err != nil {
		return err
	}
	if err = d.Set("name", orgSettings.GetName()); err != nil {
		return err
	}
	if err = d.Set("description", orgSettings.GetDescription()); err != nil {
		return err
	}
	if err = d.Set("has_organization_projects", orgSettings.GetHasOrganizationProjects()); err != nil {
		return err
	}
	if err = d.Set("has_repository_projects", orgSettings.GetHasRepositoryProjects()); err != nil {
		return err
	}
	if err = d.Set("default_repository_permission", orgSettings.GetDefaultRepoPermission()); err != nil {
		return err
	}
	if err = d.Set("members_can_create_repositories", orgSettings.GetMembersCanCreateRepos()); err != nil {
		return err
	}
	if err = d.Set("members_can_create_internal_repositories", orgSettings.GetMembersCanCreateInternalRepos()); err != nil {
		return err
	}
	if err = d.Set("members_can_create_private_repositories", orgSettings.GetMembersCanCreatePrivateRepos()); err != nil {
		return err
	}
	if err = d.Set("members_can_create_public_repositories", orgSettings.GetMembersCanCreatePublicRepos()); err != nil {
		return err
	}
	if err = d.Set("members_can_create_pages", orgSettings.GetMembersCanCreatePages()); err != nil {
		return err
	}
	if err = d.Set("members_can_create_public_pages", orgSettings.GetMembersCanCreatePublicPages()); err != nil {
		return err
	}
	if err = d.Set("members_can_create_private_pages", orgSettings.GetMembersCanCreatePrivatePages()); err != nil {
		return err
	}
	if err = d.Set("members_can_fork_private_repositories", orgSettings.GetMembersCanForkPrivateRepos()); err != nil {
		return err
	}
	if err = d.Set("web_commit_signoff_required", orgSettings.GetWebCommitSignoffRequired()); err != nil {
		return err
	}
	if err = d.Set("blog", orgSettings.GetBlog()); err != nil {
		return err
	}
	if err = d.Set("advanced_security_enabled_for_new_repositories", orgSettings.GetAdvancedSecurityEnabledForNewRepos()); err != nil {
		return err
	}
	if err = d.Set("dependabot_alerts_enabled_for_new_repositories", orgSettings.GetDependabotAlertsEnabledForNewRepos()); err != nil {
		return err
	}
	if err = d.Set("dependabot_security_updates_enabled_for_new_repositories", orgSettings.GetDependabotSecurityUpdatesEnabledForNewRepos()); err != nil {
		return err
	}
	if err = d.Set("dependency_graph_enabled_for_new_repositories", orgSettings.GetDependencyGraphEnabledForNewRepos()); err != nil {
		return err
	}
	if err = d.Set("secret_scanning_enabled_for_new_repositories", orgSettings.GetSecretScanningEnabledForNewRepos()); err != nil {
		return err
	}
	if err = d.Set("secret_scanning_push_protection_enabled_for_new_repositories", orgSettings.GetSecretScanningPushProtectionEnabledForNewRepos()); err != nil {
		return err
	}
	if err = d.Set("secret_scanning_push_protection_custom_link_enabled", orgSettings.GetSecretScanningPushProtectionCustomLinkEnabled()); err != nil {
		return err
	}
	if err = d.Set("secret_scanning_push_protection_custom_link", orgSettings.GetSecretScanningPushProtectionCustomLink()); err != nil {
		return err
	}
	if err = d.Set("deploy_keys_enabled_for_repositories", orgSettings.GetDeployKeysEnabledForRepositories()); err != nil {
		return err
	}
	return nil
}

func resourceGithubOrganizationSettingsDelete(d *schema.ResourceData, meta any) error {
	err := checkOrganization(meta)
	if err != nil {
		return err
	}

	client := meta.(*Owner).v3client
	ctx := context.WithValue(context.Background(), ctxId, d.Id())
	org := meta.(*Owner).name

	log.Printf("[DEBUG] Reverting Organization Settings to default values: %s", org)

	// Get organization info to determine if it's enterprise
	orgInfo, _, err := client.Organizations.Get(ctx, org)
	if err != nil {
		return err
	}

	// Build minimal settings with only required fields
	isEnterprise := orgInfo.GetPlan().GetName() == "enterprise"
	defaultSettings := &github.Organization{
		BillingEmail: new("email@example.com"),
	}

	// Only add enterprise-specific fields if it's an enterprise org
	if isEnterprise {
		defaultSettings.MembersCanCreateInternalRepos = new(true)
	}

	_, _, err = client.Organizations.Edit(ctx, org, defaultSettings)
	if err != nil {
		return err
	}

	return nil
}
