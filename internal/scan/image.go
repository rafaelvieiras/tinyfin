package scan

import (
	"context"

	"github.com/rafaelvieiras/tinyfin/internal/imagefetch"
)

// defaultImageFetcher downloads a metadata.Image's URL — the only network call
// Scanner makes outside of the configured metadata.Provider itself. Kept as its
// own small interface (Scanner's httpClient field) purely so scan_test.go can
// swap in a fake that doesn't touch the network, the same reasoning
// metadata.Provider itself exists for.
//
// The download itself goes through internal/imagefetch, which is shared with
// the API's person-image path: scheme allowlist, a public-address guard with an
// operator allowlist, a size ceiling and an image sniff. A provider URL is not
// trusted either — it can be pointed at by whoever runs the provider, and the
// previous implementation here read the whole body with no bound and no
// address check at all.
type defaultImageFetcher struct {
	fetcher *imagefetch.Fetcher
}

// newImageFetcher builds the fetcher with the operator's image allowlist; nil
// still yields a usable fetcher, just one that refuses every non-public
// address.
func newImageFetcher(allow []string) defaultImageFetcher {
	return defaultImageFetcher{fetcher: imagefetch.New(allow)}
}

func (f defaultImageFetcher) fetchImage(ctx context.Context, url string) ([]byte, error) {
	if f.fetcher == nil {
		// A Scanner built without New (a zero-value Config in a test) keeps
		// working, with the default guard and no allowlist.
		f.fetcher = imagefetch.New(nil)
	}
	img, err := f.fetcher.Fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	return img.Bytes, nil
}
