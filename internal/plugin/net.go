package plugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

func newGetRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "licode-plugin")
	req.Header.Set("Accept", "*/*")
	return req, nil
}

func readAllLimit(r io.Reader, n int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(n)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > n {
		return nil, fmt.Errorf("响应超过 %d 字节上限", n)
	}
	return data, nil
}
