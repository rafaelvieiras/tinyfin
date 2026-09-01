package scan

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultImageFetcher downloads a metadata.Image's URL over plain HTTP —
// the only network call Scanner makes outside of the configured
// metadata.Provider itself. Kept as its own small interface (Scanner's
// httpClient field) purely so scan_test.go can swap in a fake that
// doesn't touch the network, the same reasoning metadata.Provider itself
// exists for.
type defaultImageFetcher struct{}

func (defaultImageFetcher) fetchImage(ctx context.Context, url string) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("image download: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
