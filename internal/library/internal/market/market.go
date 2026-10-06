package market

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	baseURL         = "https://skills.sh"
	maxResponseSize = 1 << 20
)

var (
	ErrUnreachable     = errors.New("market service is unavailable")
	ErrPermission      = errors.New("market service denied access")
	ErrInvalidResponse = errors.New("invalid market response")
)

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnreachable
	}
	return response, nil
}

func readBody(ctx context.Context, response *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, maxResponseSize+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("read response: %w", ErrUnreachable)
	}
	if len(body) > maxResponseSize {
		return nil, fmt.Errorf("response exceeds size limit: %w", ErrInvalidResponse)
	}
	return body, nil
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
