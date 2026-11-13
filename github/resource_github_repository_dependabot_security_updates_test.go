package github

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v92/github"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccGithubRepositoryDependabotSecurityUpdates(t *testing.T) {
	t.Parallel()

	t.Run("enables automated security fixes without error", func(t *testing.T) {
		t.Parallel()

		randomID := acctest.RandStringFromCharSet(5, acctest.CharSetAlphaNum)
		repoName := fmt.Sprintf("%srepo-depbot-updates-%s", testResourcePrefix, randomID)
		enabled := "enabled = false"
		updatedEnabled := "enabled = true"
		config := fmt.Sprintf(`

			resource "github_repository" "test" {
				name = "%s"
				visibility = "private"
			  	auto_init = true
				vulnerability_alerts   = true
			}


			resource "github_repository_dependabot_security_updates" "test" {
			  repository  = github_repository.test.id
			  %s
			}
		`, repoName, enabled)

		checks := map[string]resource.TestCheckFunc{
			"before": resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(
					"github_repository_dependabot_security_updates.test", "enabled",
					"false",
				),
			),
			"after": resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(
					"github_repository_dependabot_security_updates.test", "enabled",
					"true",
				),
			),
		}

		resource.Test(t, resource.TestCase{
			PreCheck:          func() { skipUnauthenticated(t) },
			ProviderFactories: providerFactories,
			Steps: []resource.TestStep{
				{
					Config: config,
					Check:  checks["before"],
				},
				{
					Config: strings.Replace(config,
						enabled,
						updatedEnabled, 1),
					Check: checks["after"],
				},
			},
		})
	})

	t.Run("disables automated security fixes without error", func(t *testing.T) {
		t.Parallel()

		randomID := acctest.RandStringFromCharSet(5, acctest.CharSetAlphaNum)
		repoName := fmt.Sprintf("%srepo-depbot-updates-%s", testResourcePrefix, randomID)
		enabled := "enabled = true"
		updatedEnabled := "enabled = false"

		config := fmt.Sprintf(`

			resource "github_repository" "test" {
				name = "%s"
				visibility = "private"
			  	auto_init = true
				vulnerability_alerts   = true
			}


			resource "github_repository_dependabot_security_updates" "test" {
			  repository  = github_repository.test.id
			  %s
			}
		`, repoName, enabled)

		checks := map[string]resource.TestCheckFunc{
			"before": resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(
					"github_repository_dependabot_security_updates.test", "enabled",
					"true",
				),
			),
			"after": resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(
					"github_repository_dependabot_security_updates.test", "enabled",
					"false",
				),
			),
		}

		resource.Test(t, resource.TestCase{
			PreCheck:          func() { skipUnauthenticated(t) },
			ProviderFactories: providerFactories,
			Steps: []resource.TestStep{
				{
					Config: config,
					Check:  checks["before"],
				},
				{
					Config: strings.Replace(config,
						enabled,
						updatedEnabled, 1),
					Check: checks["after"],
				},
			},
		})
	})

	t.Run("imports automated security fixes without error", func(t *testing.T) {
		t.Parallel()

		randomID := acctest.RandStringFromCharSet(5, acctest.CharSetAlphaNum)
		repoName := fmt.Sprintf("%srepo-depbot-updates-%s", testResourcePrefix, randomID)
		config := fmt.Sprintf(`
			resource "github_repository" "test" {
			  name = "%s"
			  vulnerability_alerts   = true
			}

			resource "github_repository_dependabot_security_updates" "test" {
			  repository  = github_repository.test.id
			  enabled = false
			}
    `, repoName)

		check := resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttrSet("github_repository_dependabot_security_updates.test", "repository"),
			resource.TestCheckResourceAttrSet("github_repository_dependabot_security_updates.test", "enabled"),
			resource.TestCheckResourceAttr("github_repository_dependabot_security_updates.test", "enabled", "false"),
		)

		resource.Test(t, resource.TestCase{
			PreCheck:          func() { skipUnauthenticated(t) },
			ProviderFactories: providerFactories,
			Steps: []resource.TestStep{
				{
					Config: config,
					Check:  check,
				},
				{
					ResourceName:      "github_repository_dependabot_security_updates.test",
					ImportState:       true,
					ImportStateVerify: true,
				},
			},
		})
	})
}

func Test_resourceGithubRepositoryDependabotSecurityUpdatesRead(t *testing.T) {
	t.Parallel()

	const (
		owner    = "test-org"
		repoName = "test-repo"
	)

	uri := fmt.Sprintf("/repos/%s/%s/automated-security-fixes", owner, repoName)

	for _, tt := range []struct {
		name       string
		status     int
		body       any
		wantErr    bool
		wantID     string
		wantEnable bool
	}{
		{
			name:       "reads the setting",
			status:     http.StatusOK,
			body:       &github.AutomatedSecurityFixes{Enabled: new(true)},
			wantID:     repoName,
			wantEnable: true,
		},
		{
			name:   "drops the resource when the repository is gone",
			status: http.StatusNotFound,
			wantID: "",
		},
		{
			name:    "returns other errors",
			status:  http.StatusInternalServerError,
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := githubApiMock([]*mockResponse{
				mustGetTestMockResponse(t, uri, tt.status, tt.body),
			})
			t.Cleanup(ts.Close)

			d := schema.TestResourceDataRaw(t, resourceGithubRepositoryDependabotSecurityUpdates().Schema, map[string]any{
				"repository": repoName,
			})
			d.SetId(repoName)

			meta := &Owner{name: owner, v3client: mustCreateTestGitHubClient(t, ts.URL)}

			err := resourceGithubRepositoryDependabotSecurityUpdatesRead(d, meta)

			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error to be %v, got %v", tt.wantErr, err)
			}
			if tt.wantErr {
				return
			}
			if d.Id() != tt.wantID {
				t.Fatalf("expected id %q, got %q", tt.wantID, d.Id())
			}
			got, ok := d.Get("enabled").(bool)
			if !ok {
				t.Fatalf("expected enabled to be a bool, got %T", d.Get("enabled"))
			}
			if got != tt.wantEnable {
				t.Fatalf("expected enabled %v, got %v", tt.wantEnable, got)
			}
		})
	}
}
