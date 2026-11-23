package agentdir

import (
	"context"

	"github.com/tiaot33/mechbox/internal/library/internal/agentsettings"
)

const codexMetadataFile = agentsettings.CodexFile

type Folder struct {
	Name          string
	Path          string
	Target        string
	SkillMD       []byte
	CodexMetadata []byte
}

const maxSkillMD = 1 << 20

type Listing struct {
	Folders []Folder
	Err     error
}

func List(ctx context.Context, directory string) ([]Folder, error) {
	listed, err := ListAll(ctx, []string{directory})
	if err != nil {
		return nil, err
	}
	return listed[0].Folders, listed[0].Err
}

func ListAll(ctx context.Context, directories []string) ([]Listing, error) {
	listings := make([]Listing, len(directories))
	err := bySystem(directories, func(system fileSystem, group []string, indexes []int) error {
		listed, err := system.listAll(ctx, group)
		if err != nil {
			return err
		}
		for i, listing := range listed {
			listings[indexes[i]] = listing
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return listings, nil
}

func Committed(ctx context.Context, root, directory string) (map[string]bool, error) {
	return on(root).committed(ctx, root, directory)
}
