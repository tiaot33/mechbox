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

func List(directory string) ([]Folder, error) {
	return on(directory).list(directory)
}

func Committed(ctx context.Context, root, directory string) (map[string]bool, error) {
	return on(root).committed(ctx, root, directory)
}
