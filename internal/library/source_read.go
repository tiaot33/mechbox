package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/download"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/github"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
)

const sourceStore SourceType = "store"

const (
	maxFetchSize  = 64 << 20
	maxFetchFiles = 1000
)

func errUnsupportedSource() error {
	return &Error{Code: InvalidArgument, Message: "unsupported source type"}
}

func (l *sqliteLibrary) openSnapshot(ctx context.Context, src Source, subpath string, hint source.State) (source.Snapshot, error) {
	switch src.Type {
	case SourceLocal:
		locator, err := json.Marshal(src.Locator)
		if err != nil {
			return nil, fmt.Errorf("encode source locator: %w", err)
		}
		return local.Open(ctx, locator, maxFetchFiles, maxFetchSize)
	case SourceURL:
		return openDownload(ctx, src.Locator.URL, hint)
	case sourceStore:
		base := os.Getenv("SKILLS_STORE_URL")
		if base == "" {
			return nil, &Error{Code: NotConfigured, Message: "fetch store source: SKILLS_STORE_URL is not configured"}
		}
		if _, err := url.Parse(base); err != nil {
			return nil, &Error{Code: NotConfigured, Message: "fetch store source: SKILLS_STORE_URL is invalid", Err: err}
		}
		return openDownload(ctx, strings.TrimRight(base, "/")+"/"+url.PathEscape(src.Locator.PackageID)+"/"+url.PathEscape(src.Locator.Version), hint)
	case SourceGitHub:
		locator, err := json.Marshal(src.Locator)
		if err != nil {
			return nil, fmt.Errorf("encode source locator: %w", err)
		}
		return github.Open(ctx, l.github, locator, subpath)
	default:
		return nil, errUnsupportedSource()
	}
}

func openDownload(ctx context.Context, location string, hint source.State) (source.Snapshot, error) {
	locator, err := json.Marshal(SourceLocator{URL: location})
	if err != nil {
		return nil, fmt.Errorf("encode source locator: %w", err)
	}
	return download.Open(ctx, locator, hint, maxFetchFiles, maxFetchSize)
}
