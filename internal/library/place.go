package library

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

func (l *Library) applyChange(ctx context.Context, change *agentdir.Change, record func(*store.Tx) error) error {
	linked, err := l.linkedPlaces(ctx, 0)
	if err != nil {
		return err
	}
	if err := l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := record(tx); err != nil {
			return err
		}
		if err := l.detachLinkedPlaces(ctx, change, linked); err != nil {
			return err
		}
		return change.Commit(ctx)
	}); err != nil {
		return err
	}
	change.Done()
	return nil
}

type claimant struct {
	skillID SkillID
	name    string
}

func (l *Library) targetClaimant(ctx context.Context, id SkillID, name string, target resolvedScope) (*claimant, error) {
	claimants, err := l.store.Claimants(ctx, name, int64(id))
	if err != nil {
		return nil, err
	}
	want := agentdir.Canonical(target.directory)
	for _, other := range claimants {
		claimed, err := l.resolveRow(ctx, scopeRowOf(other.Scope))
		if err != nil {
			return nil, err
		}
		if agentdir.Canonical(claimed.directory) != want {
			continue
		}
		return &claimant{skillID: SkillID(other.SkillID), name: other.Name}, nil
	}
	return nil, nil
}

func (l *Library) placementConflict(ctx context.Context, operation string, id SkillID, name string, target resolvedScope, overwrite bool) (*Error, error) {
	other, err := l.targetClaimant(ctx, id, name, target)
	if err != nil {
		return nil, err
	}
	if other != nil && other.skillID == id {
		return preconditionError(operation, "another scope already installs this skill into the same directory", ErrorDetails{Path: target.directory}), nil
	}
	if other != nil {
		return preconditionError(operation, "another skill already installs this name into the same directory", ErrorDetails{
			Path: agentdir.FolderPath(target.directory, other.name), Name: other.name, SkillID: other.skillID,
		}), nil
	}
	path := agentdir.FolderPath(target.directory, name)
	exists, err := agentdir.Exists(path)
	if err != nil {
		return nil, fmt.Errorf("inspect target folder: %w", err)
	}
	if exists && !overwrite {
		return existsError(operation, "target already exists", ErrorDetails{Path: path}), nil
	}
	return nil, nil
}

type placement struct {
	skill    Skill
	files    []skillfile.File
	scope    resolvedScope
	replaces string
}

func (l *Library) place(ctx context.Context, placements []*placement, record func(*store.Tx) error) (err error) {
	for _, item := range placements {
		if err := item.scope.requireRoot("place skill folder"); err != nil {
			return err
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	for _, item := range placements {
		if err := change.Stage(ctx, item.scope.directory, item.skill.Name, item.files); err != nil {
			return err
		}
		if item.replaces != "" && item.replaces != item.skill.Name {
			change.Remove(agentdir.FolderPath(item.scope.directory, item.replaces))
		}
	}
	return l.applyChange(ctx, &change, record)
}

type linkedPlace struct {
	SkillScope
	state agentdir.FolderState
}

func (l *Library) linkedPlaces(ctx context.Context, id SkillID) ([]linkedPlace, error) {
	skillScopes, err := l.listSkillScopes(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	folders := make([]string, len(skillScopes))
	for i, skillScope := range skillScopes {
		folders[i] = skillScope.Folder
	}
	states, err := agentdir.StatFolders(ctx, folders)
	if err != nil {
		return nil, err
	}
	var linked []linkedPlace
	for i, skillScope := range skillScopes {
		if states[i].LinkTarget != "" {
			linked = append(linked, linkedPlace{SkillScope: skillScope, state: states[i]})
		}
	}
	return linked, nil
}

func (l *Library) detachLinkedPlaces(ctx context.Context, change *agentdir.Change, linked []linkedPlace) error {
	if len(linked) == 0 {
		return nil
	}
	targets, err := agentdir.StatFolders(ctx, change.Targets())
	if err != nil {
		return err
	}
	for _, place := range linked {
		if slices.ContainsFunc(targets, place.state.SameEntry) || !place.state.Resolves || !place.state.LinksThrough(targets...) {
			continue
		}
		if err := change.Detach(ctx, place.Folder); err != nil {
			return err
		}
	}
	return nil
}

func (l *Library) requireFreeFolder(ctx context.Context, operation string, skill Skill, target resolvedScope) error {
	path := agentdir.FolderPath(target.directory, skill.Name)
	if other, err := l.targetClaimant(ctx, skill.ID, skill.Name, target); err != nil {
		return err
	} else if other != nil && other.skillID != skill.ID {
		return preconditionError(operation, "another skill is installed in the folder with the skill's name", ErrorDetails{Path: path, Name: other.name, SkillID: other.skillID})
	}
	if exists, err := agentdir.Exists(path); err != nil {
		return err
	} else if exists {
		return existsError(operation, "a folder with the skill's name is in the way", ErrorDetails{Path: path})
	}
	return nil
}
