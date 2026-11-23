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

type removal struct {
	skillID SkillID
	folder  string
}

func (l *Library) removeFolders(ctx context.Context, tx *store.Tx, change *agentdir.Change, operation string, removals []removal, discard []string) error {
	var unique []removal
	for _, item := range removals {
		if !slices.ContainsFunc(unique, func(other removal) bool { return other.folder == item.folder }) {
			unique = append(unique, item)
		}
	}
	edited, err := editedFolders(ctx, tx, unique)
	if err != nil {
		return err
	}
	unconfirmed := slices.DeleteFunc(edited, func(folder string) bool { return slices.Contains(discard, folder) })
	if len(unconfirmed) > 0 {
		return &Error{Code: Conflict, Message: operation + ": skill folders to delete were edited and not confirmed", Details: ErrorDetails{Paths: unconfirmed}}
	}
	for _, item := range unique {
		change.Remove(item.folder)
	}
	return nil
}

func editedFolders(ctx context.Context, tx *store.Tx, removals []removal) ([]string, error) {
	folders := make([]string, len(removals))
	for i, item := range removals {
		folders[i] = item.folder
	}
	marked := make([]bool, len(removals))
	var latest map[SkillID]string
	err := agentdir.ReadFolders(ctx, folders, maxFetchFiles, maxFetchSize, func(i int, state agentdir.FolderState) error {
		if !state.Exists || state.LinkTarget != "" {
			return nil
		}
		id := removals[i].skillID
		_, head, err := tx.SkillHead(ctx, int64(id))
		if err != nil {
			return err
		}
		status, _, err := folderStatus(ctx, tx, id, head, state)
		if err != nil || status != SkillScopeModified {
			return err
		}
		if latest == nil && state.Err == nil {
			if latest, err = sourceLatestContents(ctx, tx); err != nil {
				return err
			}
		}
		if content, ok := latest[id]; ok && state.Err == nil && skillfile.Sum(state.Files) == content {
			return nil
		}
		marked[i] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	var edited []string
	for i, folder := range folders {
		if marked[i] {
			edited = append(edited, folder)
		}
	}
	return edited, nil
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
		claimed, err := l.resolveRow(ctx, other.Scope)
		if err != nil {
			return nil, err
		}
		if agentdir.Canonical(claimed.in(other.Directory).directory) != want {
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
		return preconditionError(operation, "another scope already installs this skill into the same directory", ErrorDetails{
			Path: agentdir.FolderPath(target.directory, other.name), Name: other.name, SkillID: id,
		}), nil
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

type installedPlace struct {
	skillID SkillID
	target  resolvedScope
	rows    []store.PlaceSkillScope
}

func (p installedPlace) installed() bool {
	return len(p.rows) > 0
}

func (p installedPlace) folder() string {
	return agentdir.FolderPath(p.target.directory, p.rows[0].Folder)
}

func (p installedPlace) owned() bool {
	return p.installed() && p.rows[0].Agent == p.target.Agent
}

func (l *Library) installedAt(ctx context.Context, id SkillID, target resolvedScope) (installedPlace, error) {
	rows, err := l.store.PlaceSkillScopes(ctx, int64(id), target.Scope)
	if err != nil {
		return installedPlace{}, err
	}
	place := installedPlace{skillID: id, target: target, rows: rows}
	if len(rows) > 0 {
		place.target = target.in(rows[0].Directory)
	}
	return place, nil
}

func (l *Library) readsTwice(ctx context.Context, id SkillID, target resolvedScope) ([]Scope, error) {
	agents, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	var scopes []Scope
	for _, agent := range agents {
		if !agent.Enabled || agent.Code == target.Agent || !slices.Contains(agent.Paths(target.ProjectID != nil), target.relative) {
			continue
		}
		reader := store.Scope{Agent: agent.Code, ProjectID: target.ProjectID}
		place, err := l.store.PlaceSkillScopes(ctx, int64(id), reader)
		if err != nil {
			return nil, err
		}
		if len(place) > 0 && place[0].Directory != target.relative {
			scopes = append(scopes, scopeFrom(reader))
		}
	}
	return scopes, nil
}

func (l *Library) staleLinksOf(ctx context.Context, places []installedPlace) ([][]string, error) {
	links := make([][]string, len(places))
	elsewhere, err := l.foldersElsewhere(ctx, places)
	if err != nil {
		return nil, err
	}
	type candidates struct {
		folder int
		links  []int
	}
	var folders []string
	at := map[string]int{}
	index := func(folder string) int {
		if i, ok := at[folder]; ok {
			return i
		}
		at[folder] = len(folders)
		folders = append(folders, folder)
		return at[folder]
	}
	checks := make([]candidates, len(places))
	for i, others := range elsewhere {
		if len(others) == 0 {
			continue
		}
		checks[i].folder = index(places[i].folder())
		for _, other := range others {
			checks[i].links = append(checks[i].links, index(other))
		}
	}
	if len(folders) == 0 {
		return links, nil
	}
	states, err := agentdir.StatFolders(ctx, folders)
	if err != nil {
		return nil, err
	}
	for i, check := range checks {
		for _, link := range check.links {
			if state := states[link]; staleLink(state, states[check.folder]) {
				links[i] = append(links[i], state.Path)
			}
		}
		slices.Sort(links[i])
	}
	return links, nil
}

func staleLink(state, target agentdir.FolderState) bool {
	return state.LinkTarget != "" && !state.SameEntry(target) && state.LinksThrough(target)
}

func (l *Library) foldersElsewhere(ctx context.Context, places []installedPlace) ([][]string, error) {
	elsewhere := make([][]string, len(places))
	agents, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	claimed := map[string][]store.Claimant{}
	type candidate struct {
		place     int
		directory int
	}
	var candidates []candidate
	var directories []string
	recordedAt := map[string][]int{}
	for i, place := range places {
		if !place.installed() {
			continue
		}
		name := place.rows[0].Folder
		if _, ok := claimed[name]; !ok {
			if claimed[name], err = l.store.Claimants(ctx, name, 0); err != nil {
				return nil, err
			}
			for _, other := range claimed[name] {
				resolved, err := l.resolveRow(ctx, other.Scope)
				if err != nil {
					return nil, err
				}
				recordedAt[name] = append(recordedAt[name], len(directories))
				directories = append(directories, resolved.in(other.Directory).directory)
			}
		}
		var seen []string
		for _, agent := range agents {
			paths := agent.Paths(place.target.ProjectID != nil)
			if !agent.Enabled || !slices.Contains(paths, place.target.relative) {
				continue
			}
			recognized, err := l.store.PlaceSkillScopes(ctx, int64(place.skillID), store.Scope{Agent: agent.Code, ProjectID: place.target.ProjectID})
			if err != nil {
				return nil, err
			}
			if len(recognized) == 0 || recognized[0].Directory != place.target.relative {
				continue
			}
			for _, directory := range paths {
				if directory != place.target.relative && !slices.Contains(seen, directory) {
					seen = append(seen, directory)
					candidates = append(candidates, candidate{place: i, directory: len(directories)})
					directories = append(directories, agentdir.Join(place.target.root, directory))
				}
			}
		}
	}
	canonical := agentdir.CanonicalAll(directories)
	for _, c := range candidates {
		name := places[c.place].rows[0].Folder
		recorded := slices.ContainsFunc(recordedAt[name], func(at int) bool { return canonical[at] == canonical[c.directory] })
		if !recorded {
			elsewhere[c.place] = append(elsewhere[c.place], agentdir.FolderPath(directories[c.directory], name))
		}
	}
	return elsewhere, nil
}

func sameProject(a, b *int64) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func (l *Library) placeRemovals(ctx context.Context, places []installedPlace) ([]removal, error) {
	links, err := l.staleLinksOf(ctx, places)
	if err != nil {
		return nil, err
	}
	var removals []removal
	for i, place := range places {
		removals = append(removals, place.removals(links[i])...)
	}
	return removals, nil
}

func (p installedPlace) removals(links []string) []removal {
	removals := []removal{{skillID: p.skillID, folder: p.folder()}}
	for _, link := range links {
		removals = append(removals, removal{skillID: p.skillID, folder: link})
	}
	return removals
}
