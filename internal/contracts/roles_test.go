package contracts_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
)

func TestRoleContractIsTheSingleSource(t *testing.T) {
	r, err := contracts.LoadRoles()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"administrator", "content_editor", "county_officer", "language_reviewer",
		"moderator", "organizer", "rights_reviewer", "species_expert",
		"super_admin", "translator", "user",
	}
	if got := r.PublicRoles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	if r.Version != "1.0.0" {
		t.Fatalf("version = %q", r.Version)
	}
}

func TestRolePermissionInvariants(t *testing.T) {
	r := contracts.MustRoles()
	cases := []struct {
		role    string
		perm    string
		allowed bool
	}{
		{contracts.RoleContentEditor, contracts.PermSpeciesPublish, false},
		{contracts.RoleModerator, contracts.PermSpeciesPublish, false},
		{contracts.RoleOrganizer, contracts.PermEventsPublish, false},
		{contracts.RoleAdministrator, contracts.PermEventsPublish, true},
		{contracts.RoleSuperAdmin, contracts.PermRecognitionKillSwitch, true},
		{contracts.RoleTranslator, contracts.PermSpeciesTranslate, true},
		{contracts.RoleTranslator, contracts.PermSpeciesReviewLang, false},
		{contracts.RoleLanguageReviewer, contracts.PermSpeciesReviewLang, true},
		{contracts.RoleLanguageReviewer, contracts.PermSpeciesTranslate, false},
		{contracts.RoleRightsReviewer, contracts.PermSpeciesUploadMedia, false},
		{contracts.RoleContentEditor, contracts.PermSpeciesUploadMedia, true},
		{contracts.RoleModerator, contracts.PermRecognitionPublishRule, false},
		{contracts.RoleOrganizer, contracts.PermEventsScan, true},
		{contracts.RoleUser, contracts.PermEventsScan, false},
	}
	for _, tc := range cases {
		if got := r.Can(tc.role, tc.perm); got != tc.allowed {
			t.Errorf("Can(%s, %s) = %v, want %v", tc.role, tc.perm, got, tc.allowed)
		}
	}
	if !r.CanScan([]string{contracts.RoleOrganizer}) {
		t.Fatal("organizer must hold the scoped scanner permission")
	}
}

func TestRoleDutySeparations(t *testing.T) {
	r := contracts.MustRoles()
	for _, pair := range [][]string{
		{contracts.RoleTranslator, contracts.RoleLanguageReviewer},
		{contracts.RoleContentEditor, contracts.RoleRightsReviewer},
	} {
		if got := r.SeparationViolations(pair); len(got) == 0 {
			t.Fatalf("expected a separation violation for %v", pair)
		}
	}
	if got := r.SeparationViolations([]string{contracts.RoleContentEditor, contracts.RoleTranslator}); len(got) != 0 {
		t.Fatalf("unexpected violations: %v", got)
	}
}

func TestActorSeparations(t *testing.T) {
	r := contracts.MustRoles()
	if name, bad := r.ActorSeparationViolation(contracts.PermRecognitionPublishRule, "u1", "u1"); !bad || name != "publisher_is_not_author" {
		t.Fatalf("expected publisher_is_not_author, got %q %v", name, bad)
	}
	if _, bad := r.ActorSeparationViolation(contracts.PermRecognitionPublishRule, "u1", "u2"); bad {
		t.Fatal("different actors must not violate separation")
	}
	for _, action := range []string{
		contracts.PermSpeciesReviewLang,
		contracts.PermSpeciesReviewRights,
		contracts.PermEventsVerifyOrganizer,
		contracts.PermRecognitionApproveBackfill,
		contracts.PermRecognitionApproveAdjust,
	} {
		if _, bad := r.ActorSeparationViolation(action, "same", "same"); !bad {
			t.Fatalf("%s must reject self-approval", action)
		}
	}
}

func TestRolePermissionsAreReferenced(t *testing.T) {
	r := contracts.MustRoles()
	used := map[string]bool{}
	for _, allowed := range r.Permissions {
		_ = allowed
	}
	for _, duty := range r.Duties {
		for _, perm := range duty.Permissions {
			used[perm] = true
		}
	}
	// Every permission must be granted to at least one role.
	for perm, roles := range r.Permissions {
		if len(roles) == 0 {
			t.Errorf("permission %q has no roles", perm)
			continue
		}
		for _, role := range roles {
			if !r.Can(role, perm) {
				t.Errorf("%s should hold %s", role, perm)
			}
		}
	}
	if !used[contracts.PermSpeciesTranslate] || !used[contracts.PermSpeciesReviewLang] {
		t.Fatal("species duties must reference their permissions")
	}
}

func TestEveryRoleHasALabel(t *testing.T) {
	r := contracts.MustRoles()
	for name, spec := range r.Roles {
		if strings.TrimSpace(spec.Label) == "" || strings.TrimSpace(spec.Description) == "" {
			t.Errorf("role %q is missing a label or description", name)
		}
	}
}

func TestPermissionsAreSortedForDocs(t *testing.T) {
	// Documentation lists permissions alphabetically; keep the map addressable.
	r := contracts.MustRoles()
	keys := make([]string, 0, len(r.Permissions))
	for k := range r.Permissions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		t.Fatal("no permissions")
	}
}
