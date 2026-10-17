package wellknown

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/download"
)

const Type = "well-known"

const (
	timeout   = 30 * time.Second
	maxIndex  = 4 << 20
	schemaV2  = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"
	indexFile = "index.json"
)

var wellKnownPaths = []string{".well-known/agent-skills", ".well-known/skills"}

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

var hostname = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)(?:\.(?i:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?))+$`)

type locator struct {
	Index string `json:"index"`
}

type Kind struct {
	maxFiles int
	maxBytes int64
}

func New(maxFiles int, maxBytes int64) Kind {
	return Kind{maxFiles: maxFiles, maxBytes: maxBytes}
}

func SearchInput(host, name string) (string, bool) {
	if !hostname.MatchString(host) || !discovery.ValidName(name) || strings.ContainsAny(name, "#?% ") {
		return "", false
	}
	return "https://" + strings.ToLower(host) + "/" + wellKnownPaths[0] + "/" + url.PathEscape(name), true
}

func decode(loc source.Locator) (locator, error) {
	var decoded locator
	if err := json.Unmarshal(loc, &decoded); err != nil {
		return locator{}, fmt.Errorf("decode well-known locator: %w", err)
	}
	return decoded, nil
}

func (Kind) Describe(loc source.Locator) (source.Description, error) {
	decoded, err := decode(loc)
	if err != nil {
		return source.Description{}, err
	}
	base := baseURL(decoded.Index)
	u, err := url.Parse(base)
	if err != nil {
		return source.Description{Label: base}, nil
	}
	return source.Description{Label: u.Host + strings.TrimSuffix(u.Path, "/"), ViewURL: base}, nil
}

func baseURL(index string) string {
	for _, known := range wellKnownPaths {
		if before, ok := strings.CutSuffix(index, "/"+known+"/"+indexFile); ok {
			return before
		}
	}
	return index
}

func Locate(ctx context.Context, link string) (source.Target, bool, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return source.Target{}, false, &source.InputError{Reason: "invalid link"}
	}
	base := strings.TrimSuffix(u.EscapedPath(), "/")
	var name string
	for _, known := range wellKnownPaths {
		before, rest, ok := strings.Cut(base+"/", "/"+known+"/")
		if !ok {
			continue
		}
		base = before
		rest, _, _ = strings.Cut(rest, "/")
		if rest != "" && rest != indexFile {
			if name, err = url.PathUnescape(rest); err != nil {
				return source.Target{}, false, &source.InputError{Reason: "invalid well-known skill name"}
			}
		}
		break
	}
	origin := u.Scheme + "://" + u.Host
	for _, known := range wellKnownPaths {
		location := origin + base + "/" + known + "/" + indexFile
		parsed, err := fetchIndex(ctx, location)
		if err != nil {
			return source.Target{}, false, err
		}
		if parsed == nil {
			continue
		}
		encoded, err := json.Marshal(locator{Index: location})
		if err != nil {
			return source.Target{}, false, fmt.Errorf("encode well-known locator: %w", err)
		}
		target := source.Target{Type: Type, Locator: encoded}
		if name != "" {
			target.Names = []string{name}
		}
		return target, true, nil
	}
	if base == "" {
		return source.Target{}, false, nil
	}
	for _, known := range wellKnownPaths {
		parsed, err := fetchIndex(ctx, origin+"/"+known+"/"+indexFile)
		if err != nil {
			return source.Target{}, false, err
		}
		if parsed != nil {
			path, _ := url.PathUnescape(base)
			return source.Target{}, false, &ScopeError{Path: path}
		}
	}
	return source.Target{}, false, nil
}

type ScopeError struct {
	Path string
}

func (e *ScopeError) Error() string {
	return fmt.Sprintf("no well-known skills under path %q; only the site root publishes a skills index", e.Path)
}

func (e *ScopeError) Unwrap() error {
	return &source.NotFoundError{What: source.MissingSubpath, Name: e.Path}
}

type entry struct {
	name        string
	description string
	files       []string
	kind        string
	url         string
	digest      string
}

type index struct {
	location    string
	revision    string
	entries     []entry
	diagnostics []discovery.Diagnostic
}

func fetchIndex(ctx context.Context, location string) (*index, error) {
	content, status, err := get(ctx, location, maxIndex)
	if err != nil {
		if status != 0 {
			return nil, nil
		}
		return nil, err
	}
	var raw struct {
		Schema *string           `json:"$schema"`
		Skills []json.RawMessage `json:"skills"`
	}
	if json.Unmarshal(content, &raw) != nil || raw.Skills == nil {
		return nil, nil
	}
	if raw.Schema != nil && *raw.Schema != schemaV2 {
		return nil, nil
	}
	sum := sha256.Sum256(content)
	result := &index{location: location, revision: hex.EncodeToString(sum[:])}
	for i, item := range raw.Skills {
		parsed, reason := parseEntry(item, raw.Schema != nil, location)
		label := parsed.name
		if label == "" {
			label = fmt.Sprintf("skills[%d]", i)
		}
		if reason == "" && slices.ContainsFunc(result.entries, func(e entry) bool { return e.name == parsed.name }) {
			reason = "duplicate skill name in index"
		}
		if reason != "" {
			result.diagnostics = append(result.diagnostics, discovery.Diagnostic{Path: label, Reason: reason})
			continue
		}
		result.entries = append(result.entries, parsed)
	}
	return result, nil
}

func parseEntry(item json.RawMessage, v2 bool, location string) (entry, string) {
	var raw struct {
		Name        any `json:"name"`
		Description any `json:"description"`
		Files       any `json:"files"`
		Type        any `json:"type"`
		URL         any `json:"url"`
		Digest      any `json:"digest"`
	}
	if json.Unmarshal(item, &raw) != nil {
		return entry{}, "index entry is not an object"
	}
	name, _ := raw.Name.(string)
	if !discovery.ValidName(name) {
		return entry{}, "index entry name must be a non-empty safe string"
	}
	description, ok := raw.Description.(string)
	if !ok {
		return entry{name: name}, "index entry description must be a string"
	}
	result := entry{name: name, description: description}
	if !v2 {
		list, ok := raw.Files.([]any)
		if !ok || len(list) == 0 {
			return result, "index entry must list its files"
		}
		for _, item := range list {
			file, ok := item.(string)
			if !ok || !fs.ValidPath(file) || file == "." || strings.ContainsAny(file, `\:`) || slices.Contains(result.files, file) {
				return result, "index entry lists an unsafe or duplicate file path"
			}
			result.files = append(result.files, file)
		}
		if !slices.Contains(result.files, "SKILL.md") {
			return result, "index entry does not list SKILL.md"
		}
		return result, ""
	}
	result.kind, _ = raw.Type.(string)
	if result.kind != "skill-md" && result.kind != "archive" {
		return result, "index entry type must be skill-md or archive"
	}
	result.digest, _ = raw.Digest.(string)
	if !digestPattern.MatchString(result.digest) {
		return result, "index entry digest must be sha256:<hex>"
	}
	link, _ := raw.URL.(string)
	base, _ := url.Parse(location)
	resolved, err := base.Parse(link)
	if link == "" || err != nil || (resolved.Scheme != "https" && resolved.Scheme != "http") {
		return result, "index entry url is invalid"
	}
	result.url = resolved.String()
	return result, ""
}

type statusError struct {
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("HTTP %d", e.status)
}

func get(ctx context.Context, location string, limit int64) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, 0, &source.InputError{Reason: "invalid well-known URL"}
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, fmt.Errorf("fetch well-known source: %w", source.ErrUnreachable)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var sentinel error
		switch {
		case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
			sentinel = source.ErrPermission
		case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
			sentinel = source.ErrInvalid
		default:
			sentinel = source.ErrUnreachable
		}
		return nil, response.StatusCode, fmt.Errorf("%w: %w", &statusError{response.StatusCode}, sentinel)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, fmt.Errorf("read well-known source: %w", source.ErrUnreachable)
	}
	if int64(len(content)) > limit {
		return nil, response.StatusCode, fmt.Errorf("read well-known source: %w: more than %d bytes", discovery.ErrTooLarge, limit)
	}
	return content, response.StatusCode, nil
}

func (k Kind) Open(ctx context.Context, loc source.Locator, _ string, _ source.State) (source.Snapshot, error) {
	decoded, err := decode(loc)
	if err != nil {
		return nil, err
	}
	parsed, err := fetchIndex(ctx, decoded.Index)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, fmt.Errorf("read well-known index %s: index is missing or invalid: %w", decoded.Index, source.ErrUnreachable)
	}
	return &snapshot{kind: k, index: parsed, fetched: map[string]source.Skill{}}, nil
}

type snapshot struct {
	kind    Kind
	index   *index
	fetched map[string]source.Skill
}

type state struct {
	Digest string `json:"digest,omitempty"`
}

func (s *snapshot) Revision() string {
	return s.index.revision
}

func (s *snapshot) Candidates(context.Context) ([]discovery.Candidate, []discovery.Diagnostic, error) {
	if len(s.index.entries) == 0 {
		return nil, nil, discovery.NoCandidates(s.index.diagnostics)
	}
	candidates := make([]discovery.Candidate, len(s.index.entries))
	for i, e := range s.index.entries {
		candidates[i] = discovery.Candidate{Name: e.name, Description: e.description, Path: e.name}
	}
	return candidates, s.index.diagnostics, nil
}

func (s *snapshot) Names(context.Context) ([]string, error) {
	names := make([]string, len(s.index.entries))
	for i, e := range s.index.entries {
		names[i] = e.name
	}
	return names, nil
}

func (s *snapshot) Commits(context.Context, string) ([]source.Commit, error) {
	return nil, nil
}

func (s *snapshot) find(name string) (entry, error) {
	for _, e := range s.index.entries {
		if e.name == name {
			return e, nil
		}
	}
	return entry{}, &source.NotFoundError{What: source.MissingSkill, Name: name}
}

func (s *snapshot) Fetch(ctx context.Context, names []string) ([]source.Skill, error) {
	entries := make([]entry, len(names))
	for i, name := range names {
		e, err := s.find(name)
		if err != nil {
			return nil, err
		}
		entries[i] = e
	}
	budget := discovery.NewBudget(s.kind.maxFiles, s.kind.maxBytes)
	result := make([]source.Skill, len(entries))
	for i, e := range entries {
		skill, ok := s.fetched[e.name]
		if !ok {
			var err error
			if skill, err = s.fetch(ctx, e, budget); err != nil {
				return nil, err
			}
			s.fetched[e.name] = skill
		}
		result[i] = skill
	}
	return result, nil
}

func (s *snapshot) Check(ctx context.Context, have source.Have) (source.Status, source.State, error) {
	e, err := s.find(have.Name)
	if err != nil {
		return source.Unknown, nil, err
	}
	var stored state
	if len(have.State) > 0 {
		_ = json.Unmarshal(have.State, &stored)
	}
	if e.digest != "" && stored.Digest == e.digest {
		return source.Unchanged, have.State, nil
	}
	if _, err := s.Fetch(ctx, []string{have.Name}); err != nil {
		return source.Unknown, nil, err
	}
	return source.Unknown, nil, nil
}

func (s *snapshot) fetch(ctx context.Context, e entry, budget *discovery.Budget) (source.Skill, error) {
	fail := func(file string, err error) (source.Skill, error) {
		if ctx.Err() != nil {
			return source.Skill{}, ctx.Err()
		}
		if errors.Is(err, discovery.ErrTooLarge) {
			return source.Skill{}, err
		}
		return source.Skill{}, &source.IncompleteError{Skill: e.name, File: file, Err: err}
	}
	var files []skillfile.File
	var skillState source.State
	if e.digest == "" {
		skillBase := strings.TrimSuffix(s.index.location, indexFile) + url.PathEscape(e.name) + "/"
		for _, file := range e.files {
			content, err := s.download(ctx, skillBase+escapePath(file), budget)
			if err != nil {
				return fail(file, err)
			}
			files = append(files, skillfile.File{Path: file, Content: content, Mode: skillfile.ModeRegular})
		}
	} else {
		content, err := s.download(ctx, e.url, budget)
		if err != nil {
			return fail("", err)
		}
		sum := sha256.Sum256(content)
		if "sha256:"+hex.EncodeToString(sum[:]) != e.digest {
			return fail("", fmt.Errorf("content does not match the index digest: %w", source.ErrInvalid))
		}
		if e.kind == "skill-md" {
			files = []skillfile.File{{Path: "SKILL.md", Content: content, Mode: skillfile.ModeRegular}}
		} else if files, err = unpack(ctx, content, s.kind.maxFiles, s.kind.maxBytes); err != nil {
			return fail("", err)
		}
		skillState, _ = json.Marshal(state{Digest: e.digest})
	}
	index := slices.IndexFunc(files, func(file skillfile.File) bool { return file.Path == "SKILL.md" })
	if index < 0 {
		return fail("SKILL.md", fmt.Errorf("archive has no SKILL.md at its root: %w", source.ErrInvalid))
	}
	meta, err := discovery.ParseSkillMD(files[index].Content, ".")
	if err != nil {
		return fail("SKILL.md", fmt.Errorf("%v: %w", err, source.ErrInvalid))
	}
	slices.SortFunc(files, func(a, b skillfile.File) int { return strings.Compare(a.Path, b.Path) })
	return source.Skill{Name: e.name, Description: meta.Description, Files: files, State: skillState}, nil
}

func (s *snapshot) download(ctx context.Context, location string, budget *discovery.Budget) ([]byte, error) {
	content, _, err := get(ctx, location, s.kind.maxBytes)
	if err != nil {
		return nil, err
	}
	if err := budget.Take(1, int64(len(content))); err != nil {
		return nil, err
	}
	return content, nil
}

func escapePath(file string) string {
	segments := strings.Split(file, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func unpack(ctx context.Context, content []byte, maxFiles int, maxBytes int64) ([]skillfile.File, error) {
	fsys, err := download.Unpack(ctx, content, maxFiles, maxBytes)
	if err != nil {
		if errors.Is(err, download.ErrUnrecognized) {
			return nil, fmt.Errorf("artifact is not a zip, tar or tar.gz archive: %w", source.ErrInvalid)
		}
		return nil, err
	}
	var files []skillfile.File
	err = fs.WalkDir(fsys, ".", func(current string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archive entry %q is not a regular file: %w", current, source.ErrInvalid)
		}
		data, err := fs.ReadFile(fsys, current)
		if err != nil {
			return err
		}
		files = append(files, skillfile.File{Path: current, Content: bytes.Clone(data), Mode: skillfile.ModeOf(info.Mode())})
		return nil
	})
	return files, err
}
