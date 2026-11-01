package github

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// newTestOrganizationOwner serves mux as the GitHub API of organization acme.
func newTestOrganizationOwner(t *testing.T, mux *http.ServeMux) *Owner {
	t.Helper()

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &Owner{name: "acme", IsOrganization: true, maxPerPage: 100, v3client: mustCreateTestGitHubClient(t, server.URL+"/")}
}

// callLog records API calls from concurrent handlers in arrival order.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
}

func TestImportOrganizationSingleton(t *testing.T) {
	t.Parallel()

	meta := &Owner{name: "Acme", IsOrganization: true}

	tests := []struct {
		name string
		id   string
		meta *Owner
		err  string
	}{
		{name: "matches_owner_ignoring_case", id: "acme", meta: meta},
		{name: "rejects_other_org", id: "other", meta: meta, err: `import ID "other" must be the provider's organization "Acme"`},
		{name: "rejects_user_owner", id: "someone", meta: &Owner{name: "someone"}, err: "can only be used in the context of an organization"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := schema.TestResourceDataRaw(t, resourceGithubOrganizationTeams().Schema, map[string]any{})
			d.SetId(tt.id)

			_, err := importOrganizationSingleton(t.Context(), d, tt.meta)
			if tt.err == "" {
				if err != nil || d.Id() != tt.meta.name {
					t.Fatalf("err = %v, id = %q, want no error and id %q", err, d.Id(), tt.meta.name)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.err)
			}
		})
	}
}

func TestStateOnlyDelete(t *testing.T) {
	t.Parallel()

	diags := stateOnlyDelete("removed from state only")(t.Context(), nil, &Owner{name: "acme"})
	if len(diags) != 1 || diags[0].Severity != diag.Warning || diags[0].Summary != "removed from state only" {
		t.Fatalf("diags = %v, want one warning", diags)
	}
}

func TestOrganizationTeamsCreateOrUpdate(t *testing.T) {
	t.Parallel()

	var log callLog
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orgs/acme/teams", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"slug":"keep"},{"slug":"parent"},{"slug":"child","parent":{"slug":"parent"}}]`)
	})
	mux.HandleFunc("DELETE /orgs/acme/teams/{slug}", func(w http.ResponseWriter, r *http.Request) {
		log.add(r.PathValue("slug"))
		if r.PathValue("slug") == "child" {
			// Already deleted together with its parent.
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	d := schema.TestResourceDataRaw(t, resourceGithubOrganizationTeams().Schema, map[string]any{"team_slugs": []any{"Keep"}})
	if diags := resourceGithubOrganizationTeamsCreateOrUpdate(t.Context(), d, newTestOrganizationOwner(t, mux)); diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	if !slices.Equal(log.calls, []string{"child", "parent"}) {
		t.Errorf("deleted = %v, want [child parent]", log.calls)
	}
	if d.Id() != "acme" {
		t.Errorf("id = %q, want acme", d.Id())
	}
}

func TestOrganizationMembersCreateOrUpdate(t *testing.T) {
	t.Parallel()

	newMux := func(caller string, log *callLog) *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /user", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"login":"`+caller+`"}`)
		})
		mux.HandleFunc("GET /orgs/acme/members", func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("role") {
			case "admin":
				_, _ = io.WriteString(w, `[{"login":"bot"},{"login":"jane"}]`)
			default:
				_, _ = io.WriteString(w, `[{"login":"bot"},{"login":"jane"},{"login":"leaver"}]`)
			}
		})
		mux.HandleFunc("GET /orgs/acme/invitations", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `[{"login":"invitee","role":"direct_member"},{"email":"x@example.com","role":"direct_member"}]`)
		})
		mux.HandleFunc("PUT /orgs/acme/memberships/{user}", func(w http.ResponseWriter, r *http.Request) {
			log.add("set " + r.PathValue("user"))
			_, _ = io.WriteString(w, `{}`)
		})
		mux.HandleFunc("DELETE /orgs/acme/memberships/{user}", func(w http.ResponseWriter, r *http.Request) {
			log.add("remove " + r.PathValue("user"))
			w.WriteHeader(http.StatusNoContent)
		})
		return mux
	}

	t.Run("adds_before_removing", func(t *testing.T) {
		t.Parallel()

		var log callLog
		d := schema.TestResourceDataRaw(t, resourceGithubOrganizationMembers().Schema, map[string]any{
			"members": []any{
				map[string]any{"username": "Bot", "role": "admin"},
				map[string]any{"username": "jane", "role": "member"},
				map[string]any{"username": "newbie", "role": "member"},
			},
		})

		if diags := resourceGithubOrganizationMembersCreateOrUpdate(t.Context(), d, newTestOrganizationOwner(t, newMux("bot", &log))); diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		want := []string{"set jane", "set newbie", "remove invitee", "remove leaver"}
		if !slices.Equal(log.calls, want) {
			t.Errorf("calls = %v, want %v", log.calls, want)
		}
	})

	t.Run("refuses_to_demote_the_caller", func(t *testing.T) {
		t.Parallel()

		var log callLog
		d := schema.TestResourceDataRaw(t, resourceGithubOrganizationMembers().Schema, map[string]any{
			"members": []any{
				map[string]any{"username": "jane", "role": "admin"},
				map[string]any{"username": "bot", "role": "member"},
			},
		})

		diags := resourceGithubOrganizationMembersCreateOrUpdate(t.Context(), d, newTestOrganizationOwner(t, newMux("bot", &log)))
		if !diags.HasError() || !strings.Contains(diags[0].Summary, `keep the authenticated user "bot" as an admin`) {
			t.Fatalf("diags = %v, want a caller lockout error", diags)
		}
		if len(log.calls) != 0 {
			t.Errorf("calls = %v, want none", log.calls)
		}
	})
}
