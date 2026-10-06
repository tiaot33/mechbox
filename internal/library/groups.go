package library

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

func groupOf(row store.Group) Group {
	return Group{ID: GroupID(row.ID), Name: row.Name, CreatedAt: row.CreatedAt}
}

func (l *sqliteLibrary) CreateGroup(ctx context.Context, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("create group", "name", "name is required")
	}
	taken, err := l.store.GroupNames(ctx)
	if err != nil {
		return Group{}, err
	}
	name = uniqueGroupName(name, taken)
	createdAt := clockNow()
	id, err := l.store.CreateGroup(ctx, name, createdAt)
	if err != nil {
		return Group{}, err
	}
	return Group{ID: GroupID(id), Name: name, CreatedAt: createdAt}, nil
}

func (l *sqliteLibrary) ListGroups(ctx context.Context) (groups []Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		groups = append(groups, groupOf(row))
	}
	return groups, nil
}

func (l *sqliteLibrary) RenameGroup(ctx context.Context, id GroupID, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("rename group", "name", "name is required")
	}
	row, err := l.store.GetGroup(ctx, int64(id))
	if err != nil {
		return Group{}, err
	}
	current := groupOf(row)
	if current.Name == name {
		return current, nil
	}
	taken, err := l.store.GroupNames(ctx)
	if err != nil {
		return Group{}, err
	}
	delete(taken, current.Name)
	name = uniqueGroupName(name, taken)
	if err := l.store.RenameGroup(ctx, int64(id), name); err != nil {
		return Group{}, err
	}
	current.Name = name
	return current, nil
}

func uniqueGroupName(name string, taken map[string]int64) string {
	if _, exists := taken[name]; !exists {
		return name
	}
	for n := 2; ; n++ {
		candidate := name + "#" + strconv.Itoa(n)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
}

func (l *sqliteLibrary) DeleteGroup(ctx context.Context, id GroupID) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		return tx.DeleteGroup(ctx, int64(id))
	})
}

func (l *sqliteLibrary) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) (_ []GroupMemberResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	if err := l.store.RequireSkill(ctx, int64(skillID)); err != nil {
		return nil, err
	}
	member, err := l.store.IsMember(ctx, int64(groupID), int64(skillID))
	if err != nil {
		return nil, err
	}
	if member {
		return nil, existsError("add skill to group", "skill is already a member", ErrorDetails{SkillID: skillID})
	}
	name, duplicate, err := l.store.MemberCandidate(ctx, int64(groupID), int64(skillID))
	if err != nil {
		return nil, err
	}
	installs, err := l.store.ListGroupInstalls(ctx, int64(groupID))
	if err != nil {
		return nil, err
	}
	plan := newMemberPlan("add skill to group", InstallOptions{})
	for _, install := range installs {
		target, err := l.resolveScope(ctx, "add skill to group", scopeRowOf(install.Scope).scope())
		if err != nil {
			return nil, err
		}
		if err := l.planMember(ctx, plan, skillID, name, target, duplicate); err != nil {
			return nil, err
		}
	}
	return l.applyMemberPlan(ctx, plan, func(tx *store.Tx, _ time.Time) error {
		return tx.AddMember(ctx, int64(groupID), int64(skillID))
	})
}

func (l *sqliteLibrary) RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID, opts RemoveFromGroupOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateMemberRemoval("remove skill from group", opts.IfNoOtherInstall); err != nil {
		return err
	}
	member, err := l.store.IsMember(ctx, int64(groupID), int64(skillID))
	if err != nil {
		return err
	}
	if !member {
		return &Error{Code: NotFound, Message: "remove skill from group: skill is not a member"}
	}
	skillScopes, err := l.store.MemberSkillScopes(ctx, int64(groupID), int64(skillID), nil)
	if err != nil {
		return err
	}
	directories := make([]string, len(skillScopes))
	for i, skillScope := range skillScopes {
		if directories[i], err = l.Resolve(ctx, scopeRowOf(skillScope.Scope).scope()); err != nil {
			return err
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		if err := tx.RemoveMember(ctx, int64(groupID), int64(skillID)); err != nil {
			return err
		}
		for i, skillScope := range skillScopes {
			if opts.IfNoOtherInstall != UninstallMember {
				if err := tx.KeepAsDirectInstall(ctx, skillScope.ID); err != nil {
					return err
				}
				continue
			}
			removed, err := tx.UninstallMember(ctx, skillScope.ID)
			if err != nil {
				return err
			}
			if removed {
				change.Remove(directories[i], skillScope.Name)
			}
		}
		return nil
	})
}

func (l *sqliteLibrary) ListGroupSkills(ctx context.Context, groupID GroupID) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	return summariesOf(l.store.ListGroupSummaries(ctx, int64(groupID)))
}

func (l *sqliteLibrary) ListGroupsWithMembers(ctx context.Context) (groups []GroupWithMembers, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListGroupsWithMembers(ctx)
	if err != nil {
		return nil, err
	}
	groups = []GroupWithMembers{}
	for _, row := range rows {
		group := GroupWithMembers{Group: groupOf(row.Group), Members: []SkillID{}}
		for _, id := range row.Members {
			group.Members = append(group.Members, SkillID(id))
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (l *sqliteLibrary) requireGroup(ctx context.Context, id GroupID) error {
	_, err := l.store.GetGroup(ctx, int64(id))
	return err
}
