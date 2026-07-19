// Package library manages Agent Skills in a SQLite-backed Library and projects
// them into Claude Code and Codex skill directories.
package library

import (
	"context"
	"errors"
	"time"
)

type SkillID int64
type SourceID int64
type VersionID int64
type GroupID int64
type ProjectID int64

// ErrorCode is a stable error category callers can use for UI decisions.
type ErrorCode string

const (
	NotFound          ErrorCode = "not_found"
	AlreadyExists     ErrorCode = "already_exists"
	SourceUnreachable ErrorCode = "source_unreachable"
	Permission        ErrorCode = "permission"
	Internal          ErrorCode = "internal"
)

// Error is the serializable error returned by Library operations.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Err     error     `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// IsCode reports whether err contains the given Library error code.
func IsCode(err error, code ErrorCode) bool {
	var libraryErr *Error
	return errors.As(err, &libraryErr) && libraryErr.Code == code
}

type SourceType string

const (
	SourceGitHub SourceType = "github"
	SourceURL    SourceType = "url"
	SourceStore  SourceType = "store"
	SourceLocal  SourceType = "local"
)

type FileMode string

const (
	ModeRegular    FileMode = "regular"
	ModeExecutable FileMode = "executable"
)

type LocalSource struct {
	Path string `json:"path"`
}

type SourceLocator struct {
	Repository string `json:"repository,omitempty"`
	Branch     string `json:"branch,omitempty"`
	URL        string `json:"url,omitempty"`
	PackageID  string `json:"package_id,omitempty"`
	Version    string `json:"version,omitempty"`
	Path       string `json:"path,omitempty"`
}

type Source struct {
	ID          SourceID      `json:"id"`
	Type        SourceType    `json:"type"`
	Locator     SourceLocator `json:"locator"`
	LastFetched time.Time     `json:"last_fetched"`
	LastSeenRef string        `json:"last_seen_ref"`
}

type File struct {
	Path    string   `json:"path"`
	Content []byte   `json:"content"`
	Mode    FileMode `json:"mode"`
}

type Skill struct {
	ID     SkillID `json:"id"`
	Name   string  `json:"name"`
	Source Source  `json:"source"`
	Files  []File  `json:"files"`
}

type SkillVersion struct {
	ID      VersionID `json:"id"`
	SkillID SkillID   `json:"skill_id"`
	Files   []File    `json:"files,omitempty"`
}

type VersionDiff struct {
	Changed []string `json:"changed"`
	Added   []string `json:"added"`
	Deleted []string `json:"deleted"`
}

type Group struct {
	ID   GroupID `json:"id"`
	Name string  `json:"name"`
}

type Agent string

const (
	AgentClaudeCode Agent = "claude-code"
	AgentCodex      Agent = "codex"
)

type Scope struct {
	Agent Agent  `json:"agent"`
	Path  string `json:"path"`
}

type ConflictPolicy string

const (
	Fail      ConflictPolicy = "fail"
	Overwrite ConflictPolicy = "overwrite"
)

type ActivateOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

type Activation struct {
	SkillID    SkillID   `json:"skill_id"`
	Scope      Scope     `json:"scope"`
	Individual bool      `json:"individual"`
	ViaGroups  []GroupID `json:"via_groups,omitempty"`
}

type Collection struct {
	Source Source  `json:"source"`
	Skills []Skill `json:"skills"`
}

type RegisteredProject struct {
	ID   ProjectID `json:"id"`
	Path string    `json:"path"`
}

type ProjectedCopiesPolicy string

const (
	Keep   ProjectedCopiesPolicy = "keep"
	Remove ProjectedCopiesPolicy = "remove"
)

type RemoveSkillOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"`
}

type UnregisterProjectOptions struct {
	ProjectedCopies ProjectedCopiesPolicy `json:"projected_copies"`
}

type InstallOptions struct {
	Skills []string `json:"skills,omitempty"`
}

// Library is the complete in-process API used by the future TUI. It is not an
// HTTP or RPC contract; see docs/adr/0004-backend-library-facade-seam.md.
type Library interface {
	Install(ctx context.Context, source LocalSource) (Skill, error)
	InstallRemote(ctx context.Context, source Source, opts InstallOptions) ([]Skill, error)
	InstallFetched(ctx context.Context, source Source, name string, files []File) (Skill, error)
	Update(ctx context.Context, id SkillID) (Skill, error)
	GetSkill(ctx context.Context, id SkillID) (Skill, error)
	SourceHasUpdate(ctx context.Context, id SourceID, latestRef string) (bool, error)
	ListVersions(ctx context.Context, id SkillID) ([]SkillVersion, error)
	GetVersion(ctx context.Context, id VersionID) (SkillVersion, error)
	DiffVersions(ctx context.Context, oldID, newID VersionID) (VersionDiff, error)
	CreateGroup(ctx context.Context, name string) (Group, error)
	ListGroups(ctx context.Context) ([]Group, error)
	RenameGroup(ctx context.Context, id GroupID, name string) error
	DeleteGroup(ctx context.Context, id GroupID) error
	AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) error
	RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID) error
	ListGroupSkills(ctx context.Context, groupID GroupID) ([]Skill, error)
	ListSkills(ctx context.Context) ([]Skill, error)
	RemoveSkill(ctx context.Context, id SkillID, opts RemoveSkillOptions) error
	RegisterProject(ctx context.Context, path string) (RegisteredProject, error)
	ListRegisteredProjects(ctx context.Context) ([]RegisteredProject, error)
	UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) error
	ListScopes(ctx context.Context) ([]Scope, error)
	Activate(ctx context.Context, id SkillID, scope Scope, opts ActivateOptions) error
	Deactivate(ctx context.Context, id SkillID, scope Scope) error
	ActivateGroup(ctx context.Context, groupID GroupID, scope Scope, opts ActivateOptions) error
	DeactivateGroup(ctx context.Context, groupID GroupID, scope Scope) error
	ListActivations(ctx context.Context) ([]Activation, error)
	ListCollections(ctx context.Context) ([]Collection, error)
	SyncPush(ctx context.Context) error
	SyncPull(ctx context.Context) error
	Close() error
}
