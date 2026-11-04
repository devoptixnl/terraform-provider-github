package github

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestBuildOrganizationSettingsOnCreate(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourceGithubOrganizationSettings().Schema, map[string]any{
		"billing_email":                        "billing@example.com",
		"members_can_create_repositories":      false,
		"deploy_keys_enabled_for_repositories": false,
	})

	settings := buildOrganizationSettings(d, false)

	if settings.MembersCanCreateRepos == nil || *settings.MembersCanCreateRepos {
		t.Errorf("explicit false members_can_create_repositories must be sent, got %v", settings.MembersCanCreateRepos)
	}
	if settings.DeployKeysEnabledForRepositories == nil || *settings.DeployKeysEnabledForRepositories {
		t.Errorf("explicit false deploy_keys_enabled_for_repositories must be sent, got %v", settings.DeployKeysEnabledForRepositories)
	}
	if settings.WebCommitSignoffRequired == nil || *settings.WebCommitSignoffRequired {
		t.Errorf("unconfigured web_commit_signoff_required must be sent as its default false, got %v", settings.WebCommitSignoffRequired)
	}
	if settings.SecretScanningPushProtectionCustomLinkEnabled != nil {
		t.Errorf("unconfigured secret_scanning_push_protection_custom_link_enabled must not be sent")
	}
	if settings.SecretScanningPushProtectionCustomLink != nil {
		t.Errorf("unconfigured secret_scanning_push_protection_custom_link must not be sent")
	}
}
