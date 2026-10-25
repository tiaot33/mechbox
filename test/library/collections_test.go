package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 每个来源一个集合,按首次入库的先后排列而不是按名字;只有一个技能的集合照样列出。
func TestListCollectionsListsOneCollectionPerSourceInFetchOrder(t *testing.T) {
	firstSource := filepath.Join(t.TempDir(), "z-first-skill")
	secondSource := filepath.Join(t.TempDir(), "a-second-skill")
	writeLocalSkill(t, firstSource, "# First\n")
	writeLocalSkill(t, secondSource, "# Second\n")
	lib := openTestLibrary(t)
	first, err := fetchLocalDir(t.Context(), lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocalDir(t.Context(), lib, secondSource)
	if err != nil {
		t.Fatal(err)
	}
	if first.CollectionID == 0 || first.CollectionID == second.CollectionID {
		t.Fatalf("collection IDs = %d and %d, want two distinct collections", first.CollectionID, second.CollectionID)
	}
	want := []library.Collection{
		{ID: first.CollectionID, Source: first.Source, Skills: []library.SkillID{first.ID}},
		{ID: second.CollectionID, Source: second.Source, Skills: []library.SkillID{second.ID}},
	}
	requireCollections(t, lib, want)
}

// 集合里的技能按 ID 升序,每个技能的 CollectionID 就是它所在集合的 ID。
func TestCollectionSkillsCarryTheirCollectionID(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "beta", "SKILL.md"), []byte(skillDocument("beta", "beta\n")), 0o644)
	writeFile(t, filepath.Join(root, "alpha", "SKILL.md"), []byte(skillDocument("alpha", "alpha\n")), 0o644)
	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"beta", "alpha"})
	collections, err := lib.ListCollections(ctx)
	if err != nil || len(collections) != 1 {
		t.Fatalf("ListCollections() = %#v, %v; want one collection", collections, err)
	}
	collection := collections[0]
	if want := []library.SkillID{min(fetched[0].ID, fetched[1].ID), max(fetched[0].ID, fetched[1].ID)}; !reflect.DeepEqual(collection.Skills, want) {
		t.Fatalf("collection skills = %v, want %v", collection.Skills, want)
	}
	skills, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		if skill.CollectionID != collection.ID || skill.Source != collection.Source {
			t.Fatalf("skill %s collection = %d %#v, want %d %#v", skill.Name, skill.CollectionID, skill.Source, collection.ID, collection.Source)
		}
	}
}

// 最后一个技能移除后集合不再列出;再从同一来源入库,集合的 ID 不变。
func TestCollectionDisappearsWithItsLastSkillAndKeepsItsIDOnRefetch(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "only")
	writeLocalSkill(t, source, "# Only\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(ctx, skill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	requireCollections(t, lib, []library.Collection{})
	again, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	if again.CollectionID != skill.CollectionID {
		t.Fatalf("refetched collection ID = %d, want %d", again.CollectionID, skill.CollectionID)
	}
	requireCollections(t, lib, []library.Collection{{ID: skill.CollectionID, Source: again.Source, Skills: []library.SkillID{again.ID}}})
}

func TestListCollectionsOfEmptyLibraryIsEmptyList(t *testing.T) {
	requireCollections(t, openTestLibrary(t), []library.Collection{})
}

func requireCollections(t *testing.T, lib *library.Library, want []library.Collection) {
	t.Helper()
	got, err := lib.ListCollections(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ListCollections() = %#v, %v; want %#v", got, err, want)
	}
}

func TestMarkNotInterestedValidatesInput(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "demo")
	if err := lib.MarkNotInterested(ctx, 9999, []string{"x"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("missing collection = %v", err)
	}
	if err := lib.MarkNotInterested(ctx, skill.CollectionID, nil); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("no names = %v", err)
	}
	if err := lib.MarkNotInterested(ctx, skill.CollectionID, []string{"../bad"}); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("invalid name = %v", err)
	}
}
