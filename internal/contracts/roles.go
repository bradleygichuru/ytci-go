// Package contracts loads the frozen execution contracts (roles, CloudEvents,
// OpenAPI and stream vocabularies) from the embedded api/ directory.
//
// The YAML files in api/contracts are the single source of truth. The server
// reads them at startup; tests fail the build if they drift from the database,
// the OpenAPI document or the mock fixtures.
package contracts

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/bradleygichuru/ytci-go/api"
)

// Role names. These are constrained by users_role_check (migration 0069).
const (
	RoleUser             = "user"
	RoleCountyOfficer    = "county_officer"
	RoleContentEditor    = "content_editor"
	RoleSpeciesExpert    = "species_expert"
	RoleTranslator       = "translator"
	RoleLanguageReviewer = "language_reviewer"
	RoleRightsReviewer   = "rights_reviewer"
	RoleModerator        = "moderator"
	RoleOrganizer        = "organizer"
	RoleAdministrator    = "administrator"
	RoleSuperAdmin       = "super_admin"
)

// Permission names used by the server. Keep in sync with api/contracts/roles.v1.yaml.
const (
	PermPlatformAdminAccess = "platform.admin_access"
	PermPlatformRolesManage = "platform.roles.manage"

	PermEventsRead                = "events.read"
	PermEventsCreate              = "events.create"
	PermEventsEditDraft           = "events.edit_draft"
	PermEventsSubmitReview        = "events.submit_review"
	PermEventsPublish             = "events.publish"
	PermEventsPublishPaid         = "events.publish_paid"
	PermEventsVerifyOrganizer     = "events.verify_organizer"
	PermEventsRevokeOrganizer     = "events.revoke_organizer"
	PermEventsViewRegistrations   = "events.view_registrations"
	PermEventsScan                = "events.scan"
	PermEventsOverrideCheckIn     = "events.override_checkin"
	PermEventsChangeNoticeDraft   = "events.change_notice_draft"
	PermEventsChangeNoticeApprove = "events.change_notice_approve"
	PermEventsReportDecide        = "events.report_decide"

	PermRecognitionRead            = "recognition.read"
	PermRecognitionAuthorRule      = "recognition.author_rule"
	PermRecognitionSimulateRule    = "recognition.simulate_rule"
	PermRecognitionPublishRule     = "recognition.publish_rule"
	PermRecognitionAuthorBadge     = "recognition.author_badge"
	PermRecognitionPublishBadge    = "recognition.publish_badge"
	PermRecognitionPrepareSeason   = "recognition.prepare_season"
	PermRecognitionPublishSeason   = "recognition.publish_season"
	PermRecognitionCloseSeason     = "recognition.close_season"
	PermRecognitionModerate        = "recognition.moderate"
	PermRecognitionDisqualify      = "recognition.disqualify"
	PermRecognitionAdjustXP        = "recognition.adjust_xp"
	PermRecognitionApproveAdjust   = "recognition.approve_adjustment"
	PermRecognitionBackfill        = "recognition.backfill"
	PermRecognitionApproveBackfill = "recognition.approve_backfill"
	PermRecognitionKillSwitch      = "recognition.kill_switch"
	PermRecognitionAuditRead       = "recognition.audit_read"

	PermSpeciesRead           = "species.read"
	PermSpeciesCreate         = "species.create"
	PermSpeciesEditBase       = "species.edit_base"
	PermSpeciesExpertReview   = "species.expert_review"
	PermSpeciesTranslate      = "species.translate"
	PermSpeciesReviewLang     = "species.review_language"
	PermSpeciesReviewRights   = "species.review_rights"
	PermSpeciesUploadMedia    = "species.upload_media"
	PermSpeciesPublish        = "species.publish"
	PermSpeciesUnpublish      = "species.unpublish"
	PermSpeciesRightsWithdraw = "species.rights_withdraw"

	PermAnalyticsRead          = "analytics.read"
	PermAnalyticsExport        = "analytics.export"
	PermAnalyticsErasure       = "analytics.erasure"
	PermAnalyticsSupportStream = "analytics.support_stream"

	PermThenNowRead     = "then_now.read"
	PermThenNowAuthor   = "then_now.author"
	PermThenNowReview   = "then_now.review"
	PermThenNowPublish  = "then_now.publish"
	PermThenNowTakedown = "then_now.takedown"

	PermCompetitionRead    = "competition.read"
	PermCompetitionEdit    = "competition.edit"
	PermCompetitionPublish = "competition.publish"
	PermCompetitionScore   = "competition.score"
)

// RoleSpec describes one actor role.
type RoleSpec struct {
	Label       string `yaml:"label"`
	Scope       string `yaml:"scope"`
	Description string `yaml:"description"`
}

// DutySpec groups permissions that carry a conflict-of-interest obligation.
type DutySpec struct {
	Permissions []string `yaml:"permissions"`
	Roles       []string `yaml:"roles"`
	Source      string   `yaml:"source"`
}

// RoleSeparation forbids one actor from holding two duties at once.
type RoleSeparation struct {
	Name       string   `yaml:"name"`
	CannotHold []string `yaml:"cannot_hold"`
	Rationale  string   `yaml:"rationale"`
}

// ActorSeparation forbids one actor from performing an action on their own
// prior action.
type ActorSeparation struct {
	Name        string `yaml:"name"`
	Action      string `yaml:"action"`
	PriorAction string `yaml:"prior_action"`
	Rationale   string `yaml:"rationale"`
}

// Roles is the parsed role contract.
type Roles struct {
	Version          string              `yaml:"version"`
	Owner            string              `yaml:"owner"`
	Roles            map[string]RoleSpec `yaml:"roles"`
	Permissions      map[string][]string `yaml:"permissions"`
	Duties           map[string]DutySpec `yaml:"duties"`
	AssignmentDuties map[string]DutySpec `yaml:"assignment_duties"`
	Separations      []RoleSeparation    `yaml:"separations"`
	ActorSeparations []ActorSeparation   `yaml:"actor_separations"`
}

var (
	rolesOnce sync.Once
	roles     *Roles
	rolesErr  error
)

// LoadRoles parses and validates the embedded role contract.
func LoadRoles() (*Roles, error) {
	rolesOnce.Do(func() {
		roles, rolesErr = parseRoles(api.FS)
	})
	return roles, rolesErr
}

// MustRoles returns the role contract or panics. Call it at startup only,
// after CI has validated the contract.
func MustRoles() *Roles {
	r, err := LoadRoles()
	if err != nil {
		panic(err)
	}
	return r
}

func parseRoles(fsys fs.FS) (*Roles, error) {
	raw, err := fs.ReadFile(fsys, api.RolesPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", api.RolesPath, err)
	}
	var r Roles
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", api.RolesPath, err)
	}
	if err := r.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", api.RolesPath, err)
	}
	return &r, nil
}

func (r *Roles) validate() error {
	if r.Version == "" {
		return fmt.Errorf("version is required")
	}
	if len(r.Roles) == 0 || len(r.Permissions) == 0 {
		return fmt.Errorf("roles and permissions are required")
	}
	for perm, allowed := range r.Permissions {
		if strings.TrimSpace(perm) == "" {
			return fmt.Errorf("empty permission name")
		}
		if len(allowed) == 0 {
			return fmt.Errorf("permission %q has no roles", perm)
		}
		for _, role := range allowed {
			if _, ok := r.Roles[role]; !ok {
				return fmt.Errorf("permission %q names unknown role %q", perm, role)
			}
		}
	}
	for name, duty := range r.Duties {
		for _, perm := range duty.Permissions {
			if _, ok := r.Permissions[perm]; !ok {
				return fmt.Errorf("duty %q names unknown permission %q", name, perm)
			}
		}
		for _, role := range duty.Roles {
			if _, ok := r.Roles[role]; !ok {
				return fmt.Errorf("duty %q names unknown role %q", name, role)
			}
		}
	}
	for name, duty := range r.AssignmentDuties {
		for _, perm := range duty.Permissions {
			if _, ok := r.Permissions[perm]; !ok {
				return fmt.Errorf("assignment duty %q names unknown permission %q", name, perm)
			}
		}
	}
	for _, s := range r.Separations {
		if len(s.CannotHold) != 2 {
			return fmt.Errorf("separation %q must name exactly two duties", s.Name)
		}
		for _, duty := range s.CannotHold {
			if _, ok := r.Duties[duty]; !ok {
				return fmt.Errorf("separation %q names unknown duty %q", s.Name, duty)
			}
		}
	}
	for _, s := range r.ActorSeparations {
		if _, ok := r.Permissions[s.Action]; !ok {
			return fmt.Errorf("actor separation %q names unknown action %q", s.Name, s.Action)
		}
		if _, ok := r.Permissions[s.PriorAction]; !ok {
			return fmt.Errorf("actor separation %q names unknown prior action %q", s.Name, s.PriorAction)
		}
	}
	return nil
}

// Can reports whether the role holds the permission.
func (r *Roles) Can(role, permission string) bool {
	for _, allowed := range r.Permissions[permission] {
		if allowed == role {
			return true
		}
	}
	return false
}

// CanAny reports whether any role holds the permission.
func (r *Roles) CanAny(subjectRoles []string, permission string) bool {
	for _, role := range subjectRoles {
		if r.Can(role, permission) {
			return true
		}
	}
	return false
}

// DutiesFor returns the duty names held by the role set.
func (r *Roles) DutiesFor(subjectRoles []string) []string {
	held := map[string]struct{}{}
	for _, role := range subjectRoles {
		for name, duty := range r.Duties {
			for _, dr := range duty.Roles {
				if dr == role {
					held[name] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(held))
	for name := range held {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// SeparationViolations returns every role-level pair the actor cannot hold.
func (r *Roles) SeparationViolations(subjectRoles []string) []string {
	held := map[string]struct{}{}
	for _, name := range r.DutiesFor(subjectRoles) {
		held[name] = struct{}{}
	}
	var out []string
	for _, s := range r.Separations {
		_, a := held[s.CannotHold[0]]
		_, b := held[s.CannotHold[1]]
		if a && b {
			out = append(out, s.Name)
		}
	}
	return out
}

// ActorSeparationViolation reports the separation rule broken when actor
// performs action after priorActor performed priorAction.
func (r *Roles) ActorSeparationViolation(action, actor, priorActor string) (string, bool) {
	if actor == "" || priorActor == "" || actor != priorActor {
		return "", false
	}
	for _, s := range r.ActorSeparations {
		if s.Action == action {
			return s.Name, true
		}
	}
	return "", false
}

// CanScan reports whether the actor may use the staff scanner for an
// organizer. Assignment is checked separately against organizer_staff.
func (r *Roles) CanScan(subjectRoles []string) bool {
	return r.CanAny(subjectRoles, PermEventsScan)
}

// PublicRoles returns role names sorted for documentation and tests.
func (r *Roles) PublicRoles() []string {
	out := make([]string, 0, len(r.Roles))
	for name := range r.Roles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
