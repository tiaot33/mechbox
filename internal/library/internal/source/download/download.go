package download

import (
	"archive/zip"
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
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
)

const timeout = 30 * time.Second

const Type = "url"

type locator struct {
	URL string `json:"url"`
}

type state struct {
	Validator string `json:"validator,omitempty"`
	Revision  string `json:"revision,omitempty"`
}

type snapshot struct {
	url         string
	maxFiles    int
	maxBytes    int64
	sent        string
	notModified bool
	loaded      bool
	skills      []discovery.Skill
	diagnostics []discovery.Diagnostic
	revision    string
	validator   string
}

func Locate(url string) (source.Target, error) {
	encoded, err := json.Marshal(locator{URL: url})
	if err != nil {
		return source.Target{}, fmt.Errorf("encode url locator: %w", err)
	}
	return source.Target{Type: Type, Locator: encoded}, nil
}

type Kind struct {
	maxFiles int
	maxBytes int64
}

func New(maxFiles int, maxBytes int64) Kind {
	return Kind{maxFiles: maxFiles, maxBytes: maxBytes}
}

func (k Kind) Describe(loc source.Locator) (source.Description, error) {
	decoded, err := decode(loc)
	if err != nil {
		return source.Description{}, err
	}
	return source.Description{Label: decoded.URL}, nil
}

func (k Kind) Open(ctx context.Context, loc source.Locator, _ string, hint source.State) (source.Snapshot, error) {
	decoded, err := decode(loc)
	if err != nil {
		return nil, err
	}
	s := &snapshot{url: decoded.URL, maxFiles: k.maxFiles, maxBytes: k.maxBytes}
	if err := s.read(ctx, decodeState(hint)); err != nil {
		return nil, err
	}
	if s.notModified {
		s.revision = decodeRevision(hint)
	}
	return s, nil
}

func decode(loc source.Locator) (locator, error) {
	var decoded locator
	if err := json.Unmarshal(loc, &decoded); err != nil {
		return locator{}, fmt.Errorf("decode url locator: %w", err)
	}
	return decoded, nil
}

func (s *snapshot) Revision() string {
	return s.revision
}

func (s *snapshot) Candidates(ctx context.Context) ([]discovery.Candidate, []discovery.Diagnostic, error) {
	if err := s.load(ctx); err != nil {
		return nil, nil, err
	}
	candidates := make([]discovery.Candidate, len(s.skills))
	for i, skill := range s.skills {
		candidates[i] = skill.Candidate
	}
	return candidates, s.diagnostics, nil
}

func (s *snapshot) Names(ctx context.Context) ([]string, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	names := make([]string, len(s.skills))
	for i, skill := range s.skills {
		names[i] = skill.Name
	}
	return names, nil
}

func (s *snapshot) Commits(context.Context, string) ([]source.Commit, error) {
	return nil, nil
}

func (s *snapshot) Fetch(ctx context.Context, names []string) ([]source.Skill, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	for _, name := range names {
		if !slices.ContainsFunc(s.skills, func(skill discovery.Skill) bool { return skill.Name == name }) {
			return nil, &source.NotFoundError{What: source.MissingSkill, Name: name}
		}
	}
	encoded := encodeState(s.validator, s.revision)
	var result []source.Skill
	for _, skill := range s.skills {
		if slices.Contains(names, skill.Name) {
			result = append(result, source.Skill{Name: skill.Name, Description: skill.Description, Files: skill.Files, State: encoded})
		}
	}
	return result, nil
}

func (s *snapshot) Check(ctx context.Context, have source.Have) (source.Status, source.State, error) {
	if s.notModified && decodeState(have.State) == s.sent {
		return source.Unchanged, have.State, nil
	}
	if _, err := s.Fetch(ctx, []string{have.Name}); err != nil {
		return source.Unknown, nil, err
	}
	return source.Unknown, nil, nil
}

func (s *snapshot) load(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	return s.read(ctx, "")
}

func (s *snapshot) read(ctx context.Context, validator string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return &source.InputError{Reason: "fetch source: invalid source URL"}
	}
	applyValidator(req, validator)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("fetch source: %w", source.ErrPermission)
		}
		return fmt.Errorf("fetch source: %w", source.ErrUnreachable)
	}
	defer response.Body.Close()
	if validator != "" && response.StatusCode == http.StatusNotModified {
		s.notModified, s.sent = true, validator
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return fmt.Errorf("fetch source: HTTP %d: %w", response.StatusCode, source.ErrPermission)
		}
		return fmt.Errorf("fetch source: HTTP %d: %w", response.StatusCode, source.ErrUnreachable)
	}
	content, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, s.maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read source: response could not be read: %w", source.ErrUnreachable)
	}
	if int64(len(content)) > s.maxBytes {
		return fmt.Errorf("read source: archive exceeds size limit: %w", source.ErrInvalid)
	}
	skills, diagnostics, err := s.readArchive(ctx, content)
	if err != nil {
		return err
	}
	if len(skills) == 0 {
		return discovery.NoCandidates(diagnostics)
	}
	sum := sha256.Sum256(content)
	s.skills, s.diagnostics = skills, diagnostics
	s.revision = hex.EncodeToString(sum[:])
	s.validator = responseValidator(response.Header)
	s.loaded, s.notModified = true, false
	return nil
}

func (s *snapshot) readArchive(ctx context.Context, content []byte) ([]discovery.Skill, []discovery.Diagnostic, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, nil, fmt.Errorf("read source archive: %v: %w", err, source.ErrInvalid)
	}
	if len(reader.File) > s.maxFiles {
		return nil, nil, fmt.Errorf("read source archive: archive exceeds file count limit: %w", source.ErrInvalid)
	}
	seen := make(map[string]bool)
	remaining := uint64(s.maxBytes)
	for _, archived := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		name := strings.TrimSuffix(archived.Name, "/")
		if name == "" || name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) || seen[name] {
			return nil, nil, fmt.Errorf("read source archive: unsafe or duplicate path: %w", source.ErrInvalid)
		}
		seen[name] = true
		if archived.UncompressedSize64 > remaining {
			return nil, nil, fmt.Errorf("read source archive: expanded content exceeds size limit: %w", source.ErrInvalid)
		}
		remaining -= archived.UncompressedSize64
	}
	return discovery.ScanFS(ctx, reader, ".", nil)
}

func responseValidator(header http.Header) string {
	if etag := header.Get("ETag"); etag != "" {
		return etag
	}
	if modified := header.Get("Last-Modified"); modified != "" {
		if _, err := http.ParseTime(modified); err == nil {
			return modified
		}
	}
	return ""
}

func applyValidator(req *http.Request, validator string) {
	if validator == "" {
		return
	}
	if _, err := http.ParseTime(validator); err == nil {
		req.Header.Set("If-Modified-Since", validator)
		return
	}
	req.Header.Set("If-None-Match", validator)
}

func decodeState(raw source.State) string {
	return decodeFull(raw).Validator
}

func decodeRevision(raw source.State) string {
	return decodeFull(raw).Revision
}

func decodeFull(raw source.State) state {
	var decoded state
	if len(raw) == 0 || json.Unmarshal(raw, &decoded) != nil {
		return state{}
	}
	return decoded
}

func encodeState(validator, revision string) source.State {
	if validator == "" {
		return nil
	}
	encoded, _ := json.Marshal(state{Validator: validator, Revision: revision})
	return encoded
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
