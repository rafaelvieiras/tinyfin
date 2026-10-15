package scan

import (
	"context"
	"encoding/json"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// libOptions are the members of a library's LibraryOptions a scan applies
// (spec: scan.options.*). Every other member is stored and echoed back but
// has no effect on what a scan catalogs.
type libOptions struct {
	// EnablePhotos: pictures of a home-videos library are cataloged (default
	// true); off, they are not, and a folder of pictures is a plain Folder.
	photos bool
	// SeasonZeroDisplayName names season 0 (default "Specials").
	seasonZero string
	// EnableEmbeddedTitles: a video's name is the title tag inside the file
	// when it has one (default false).
	embeddedTitles bool
}

func parseLibOptions(raw []byte) libOptions {
	o := libOptions{photos: true, seasonZero: "Specials"}
	if len(raw) == 0 {
		return o
	}
	var in struct {
		EnablePhotos          *bool   `json:"EnablePhotos"`
		SeasonZeroDisplayName *string `json:"SeasonZeroDisplayName"`
		EnableEmbeddedTitles  *bool   `json:"EnableEmbeddedTitles"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return o
	}
	if in.EnablePhotos != nil {
		o.photos = *in.EnablePhotos
	}
	if in.SeasonZeroDisplayName != nil && *in.SeasonZeroDisplayName != "" {
		o.seasonZero = *in.SeasonZeroDisplayName
	}
	if in.EnableEmbeddedTitles != nil {
		o.embeddedTitles = *in.EnableEmbeddedTitles
	}
	return o
}

type libOptionsKey struct{}

// withLibOptions carries lib's applicable options through a pass.
func withLibOptions(ctx context.Context, lib storage.Library) context.Context {
	return context.WithValue(ctx, libOptionsKey{}, parseLibOptions(lib.LibraryOptions))
}

// optionsOf returns the options of the running pass (the defaults outside one).
func optionsOf(ctx context.Context) libOptions {
	if o, ok := ctx.Value(libOptionsKey{}).(libOptions); ok {
		return o
	}
	return parseLibOptions(nil)
}
