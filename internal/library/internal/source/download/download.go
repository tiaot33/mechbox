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

var ErrInvalidURL = errors.New("invalid source URL")

type locator struct {
	URL string `json:"url"`
}

type state struct {
	Validator string `json:"validator,omitempty"`
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

func Open(ctx context.Context, loc source.Locator, hint source.State, maxFiles int, maxBytes int64) (source.Snapshot, error) {
	var decoded locator
	if err := json.Unmarshal(loc, &decoded); err != nil {
		return nil, fmt.Errorf("decode url locator: %w", err)
	}
	s := &snapshot{url: decoded.URL, maxFiles: maxFiles, maxBytes: maxBytes}
	if err := s.read(ctx, decodeState(hint)); err != nil {
		return nil, err
	}
	return s, nil
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

func (s *snapshot) Fetch(ctx context.Context, names []string) ([]source.Skill, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	for _, name := range names {
		if !slices.ContainsFunc(s.skills, func(skill discovery.Skill) bool { return skill.Name == name }) {
			return nil, &source.SkillNotFoundError{Name: name}
		}
	}
	encoded := encodeState(s.validator)
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
		return ErrInvalidURL
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
	s.loaded = true
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
	var decoded state
	if len(raw) == 0 || json.Unmarshal(raw, &decoded) != nil {
		return ""
	}
	return decoded.Validator
}

func encodeState(validator string) source.State {
	if validator == "" {
		return nil
	}
	encoded, _ := json.Marshal(state{Validator: validator})
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
