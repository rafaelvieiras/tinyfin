// localization.go implements three read-only "populate a dropdown/checklist"
// endpoints jellyfin-web's dashboard "Add Media Library"/"Manage Library"
// modal calls unconditionally on open: GET /Localization/cultures, GET
// /Localization/countries, and GET /Libraries/AvailableOptions. Confirmed
// live (404 today) against a running jellyfin-web build: the modal's entire
// "Library Settings" section (language/country pickers, real-time
// monitoring, metadata downloaders, image fetchers, trickplay, chapters,
// ...) is gated client-side on all three requests succeeding — a missing
// field degrades gracefully, but a failed request makes the section not
// render at all. None of the three requires admin on the reference server
// (confirmed live with a non-admin-shaped request: elevation is enforced,
// same as everywhere else in this package, by which routes.go wires a
// handler behind — requireAuth here, not requireAdmin), so these are wired
// with requireAuth, matching every other "list of options" GET in this
// package.
//
// # Cultures/countries: real ISO 639-1/3166 reference data, not the
// reference server's IP
//
// Confirmed live against the reference server (docs/04's sanctioned
// black-box verification): DisplayName/Name/TwoLetterISOLanguageName/
// ThreeLetterISOLanguageName(s) for cultures, DisplayName/Name/
// TwoLetterISORegionName/ThreeLetterISORegionName for countries. The
// *content* of both lists is a standard reference dataset (ISO 639-1
// language names/codes, ISO 3166 country names/codes) that predates and is
// independent of the reference server's own code — using the exact values
// observed live is using public reference data, not copying the reference
// server's expression (AGENTS.md's clean-room boundary is about the latter,
// never the former). cultures/countries.go below hold the full lists
// (192/139 entries) rather than a hand-trimmed subset: no risk of
// "inventing capability" the way a fetcher list would carry (this task's
// AvailableOptions scope decision below), since a longer, standards-derived
// dropdown is strictly more useful than a shorter one and costs nothing to
// keep complete.
package api

import (
	"net/http"

	culturepkg "github.com/rafaelvieiras/tinyfin/internal/culture"
)

type culture = culturepkg.Culture

type country struct {
	Name                     string `json:"Name"`
	DisplayName              string `json:"DisplayName"`
	TwoLetterISORegionName   string `json:"TwoLetterISORegionName"`
	ThreeLetterISORegionName string `json:"ThreeLetterISORegionName"`
}

// handleLocalizationCultures serves GET /Localization/cultures.
func (s *Server) handleLocalizationCultures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, cultures)
}

// handleLocalizationCountries serves GET /Localization/countries.
func (s *Server) handleLocalizationCountries(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, countries)
}

// libraryTypeOptions is one TypeOptions entry — per-BaseItem-kind metadata/
// image fetcher availability plus the image-slot facts the dashboard's
// image-options sub-panel renders (SupportedImageTypes/DefaultImageOptions).
// Field names and nesting confirmed live against the reference server's
// GET /Libraries/AvailableOptions.
type libraryTypeOptions struct {
	Type                string               `json:"Type"`
	MetadataFetchers    []libraryFetcherInfo `json:"MetadataFetchers"`
	ImageFetchers       []libraryFetcherInfo `json:"ImageFetchers"`
	SupportedImageTypes []string             `json:"SupportedImageTypes"`
	DefaultImageOptions []libraryImageOption `json:"DefaultImageOptions"`
}

type libraryFetcherInfo struct {
	Name           string `json:"Name"`
	DefaultEnabled bool   `json:"DefaultEnabled"`
}

type libraryImageOption struct {
	Type     string `json:"Type"`
	Limit    int    `json:"Limit"`
	MinWidth int    `json:"MinWidth"`
}

// libraryAvailableOptions is GET /Libraries/AvailableOptions's top-level
// envelope, confirmed live.
type libraryAvailableOptions struct {
	MetadataSavers        []libraryFetcherInfo `json:"MetadataSavers"`
	MetadataReaders       []libraryFetcherInfo `json:"MetadataReaders"`
	SubtitleFetchers      []libraryFetcherInfo `json:"SubtitleFetchers"`
	LyricFetchers         []libraryFetcherInfo `json:"LyricFetchers"`
	MediaSegmentProviders []libraryFetcherInfo `json:"MediaSegmentProviders"`
	TypeOptions           []libraryTypeOptions `json:"TypeOptions"`
}

// tmdbFetcher is the one real entry this project ever puts in a
// MetadataFetchers/ImageFetchers list — see the package doc comment and
// handleLibrariesAvailableOptions' doc comment for why nothing else the
// reference server would list (OMDb, Embedded Image Extractor, Screen
// Grabber, MusicBrainz, TheAudioDB, ...) appears here.
var tmdbFetcher = libraryFetcherInfo{Name: "TheMovieDb", DefaultEnabled: true}

// nfoReader is this project's one real MetadataReaders entry — internal/
// scan/nfo.go parses Kodi/Jellyfin .nfo sidecar files for Movie/Series/
// Episode items (applyMovieNFO/applySeriesNFO/applyEpisodeNFO). Never a
// MetadataSaver: nothing in this project writes a .nfo (or any other
// sidecar) back to disk, so that list is always empty below, unlike the
// reference server's own "Nfo"/"Emby Xml" saver entries.
var nfoReader = libraryFetcherInfo{Name: "Nfo", DefaultEnabled: true}

// libraryTypeOptionsByType maps every BaseItem "Type" string the reference
// server's own AvailableOptions returns (confirmed live, one request per
// LibraryContentType value jellyfin-web's library-edit form can send) to
// this project's honest TypeOptions entry for it.
//
// MetadataFetchers/ImageFetchers only ever contain tmdbFetcher, and only
// for the four kinds internal/metadata.TMDBProvider actually implements
// (Movie/Series/Season/Episode — metadata.go's Kind enum). Every other
// type — MusicArtist/MusicAlbum/Audio/MusicVideo/BoxSet/Video/Photo/Book/
// Playlist — gets empty fetcher lists: the reference server lists OMDb,
// MusicBrainz, TheAudioDB, Embedded Image Extractor, Screen Grabber, ... for
// these; this project has none of them (internal/metadata has exactly one
// provider, TMDB, and internal/scan doesn't even walk music/boxset/
// home-video/book/playlist library types yet — scan.go's CollectionType
// switch only handles Movies/TVShows today). Advertising a fetcher this
// project can't run would make the dashboard's checklist lie about what
// enabling it does — "minimal over guessed" per this task's own
// instruction, same convention library_admin.go's doc comment uses for its
// own documented gaps.
//
// SupportedImageTypes/DefaultImageOptions are copied from the confirmed
// live response as-is for every type, including ones with no fetcher: they
// describe which image *slots* a BaseItem kind conceptually has and the
// reference server's own recommended limits/MinWidth for each — reference
// facts about the wire protocol's item model, not a claim about what
// Tinyfin can fetch (that's MetadataFetchers/ImageFetchers' job, handled
// above). A library with no configured fetcher for a type still benefits
// from the dashboard showing the right image-type checkboxes, e.g. for
// manually-placed local artwork.
var libraryTypeOptionsByType = map[string]libraryTypeOptions{
	"Movie": {
		Type:                "Movie",
		MetadataFetchers:    []libraryFetcherInfo{tmdbFetcher},
		ImageFetchers:       []libraryFetcherInfo{tmdbFetcher},
		SupportedImageTypes: []string{"Primary", "Backdrop", "Logo", "Thumb"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", Limit: 1, MinWidth: 1280},
			{Type: "Art"},
			{Type: "Disc"},
			{Type: "Primary", Limit: 1},
			{Type: "Banner"},
			{Type: "Thumb", Limit: 1},
			{Type: "Logo", Limit: 1},
		},
	},
	"Series": {
		Type:                "Series",
		MetadataFetchers:    []libraryFetcherInfo{tmdbFetcher},
		ImageFetchers:       []libraryFetcherInfo{tmdbFetcher},
		SupportedImageTypes: []string{"Primary", "Backdrop", "Logo", "Thumb"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", Limit: 1, MinWidth: 1280},
			{Type: "Art"},
			{Type: "Primary", Limit: 1},
			{Type: "Banner", Limit: 1},
			{Type: "Thumb", Limit: 1},
			{Type: "Logo", Limit: 1},
		},
	},
	"Season": {
		Type:                "Season",
		MetadataFetchers:    []libraryFetcherInfo{tmdbFetcher},
		ImageFetchers:       []libraryFetcherInfo{tmdbFetcher},
		SupportedImageTypes: []string{"Primary"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", MinWidth: 1280},
			{Type: "Primary", Limit: 1},
			{Type: "Banner"},
			{Type: "Thumb"},
		},
	},
	"Episode": {
		Type:                "Episode",
		MetadataFetchers:    []libraryFetcherInfo{tmdbFetcher},
		ImageFetchers:       []libraryFetcherInfo{tmdbFetcher},
		SupportedImageTypes: []string{"Primary"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", MinWidth: 1280},
			{Type: "Primary", Limit: 1},
		},
	},
	"MusicArtist": {
		Type:                "MusicArtist",
		SupportedImageTypes: []string{"Primary", "Logo", "Banner", "Backdrop"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", Limit: 1, MinWidth: 1280},
			{Type: "Banner"},
			{Type: "Art"},
			{Type: "Logo", Limit: 1},
		},
	},
	"MusicAlbum": {
		Type:                "MusicAlbum",
		SupportedImageTypes: []string{"Primary", "Disc"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", MinWidth: 1280},
			{Type: "Disc"},
		},
	},
	"Audio": {
		Type:                "Audio",
		SupportedImageTypes: []string{"Primary"},
		DefaultImageOptions: []libraryImageOption{},
	},
	"MusicVideo": {
		Type:                "MusicVideo",
		SupportedImageTypes: []string{"Primary", "Backdrop", "Logo"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", Limit: 1, MinWidth: 1280},
			{Type: "Art"},
			{Type: "Disc"},
			{Type: "Primary", Limit: 1},
			{Type: "Banner"},
			{Type: "Thumb", Limit: 1},
			{Type: "Logo", Limit: 1},
		},
	},
	"BoxSet": {
		Type:                "BoxSet",
		SupportedImageTypes: []string{"Primary", "Backdrop", "Thumb"},
		DefaultImageOptions: []libraryImageOption{
			{Type: "Backdrop", Limit: 1, MinWidth: 1280},
			{Type: "Primary", Limit: 1},
			{Type: "Thumb", Limit: 1},
			{Type: "Logo", Limit: 1},
			{Type: "Art"},
			{Type: "Disc"},
			{Type: "Banner"},
		},
	},
	"Video": {
		Type:                "Video",
		SupportedImageTypes: []string{"Primary", "Backdrop", "Logo"},
		DefaultImageOptions: []libraryImageOption{},
	},
	"Photo": {
		Type:                "Photo",
		SupportedImageTypes: []string{},
		DefaultImageOptions: []libraryImageOption{},
	},
	"Book": {
		Type:                "Book",
		SupportedImageTypes: []string{},
		DefaultImageOptions: []libraryImageOption{},
	},
	"Playlist": {
		Type:                "Playlist",
		SupportedImageTypes: []string{},
		DefaultImageOptions: []libraryImageOption{},
	},
}

// libraryContentTypeTypes maps LibraryContentType query values (jellyfin-web
// sends the same strings storage.CollectionType's constants use, plus a few
// this project has no CollectionType constant for yet — confirmed live, one
// request per value) to the ordered list of "Type" keys
// libraryTypeOptionsByType holds for it. Unset/unrecognized
// LibraryContentType falls back to the "mixed" set (mirrors the reference
// server's own unset-param behavior, confirmed live: GET
// /Libraries/AvailableOptions?IsNewLibrary=false with no LibraryContentType
// returns exactly the same four types as LibraryContentType=mixed).
var libraryContentTypeTypes = map[string][]string{
	"movies":      {"Movie"},
	"tvshows":     {"Series", "Season", "Episode"},
	"music":       {"MusicArtist", "MusicAlbum", "Audio", "MusicVideo"},
	"boxsets":     {"BoxSet"},
	"musicvideos": {"MusicVideo"},
	"homevideos":  {"Video", "Photo"},
	"books":       {"Book"},
	"mixed":       {"Series", "Season", "Episode", "Movie"},
	"playlists":   {"Playlist"},
}

// handleLibrariesAvailableOptions serves GET /Libraries/AvailableOptions —
// not covered by the published @jellyfin/sdk client bundle (it's assembled
// client-side from separate path segments rather than shipped as a single
// generated operation, per this task's own briefing), so its route shape
// (query params IsNewLibrary/LibraryContentType) and response shape were
// both confirmed live against the reference server instead.
//
// IsNewLibrary is accepted (matching the reference server's own query
// param) but not read: this project's response never varies by it — every
// live probe with IsNewLibrary=false returned identical content to what an
// equivalent IsNewLibrary=true request would (the reference server appears
// to use it for something else, e.g. UI-only wording, not response shape),
// and this project has no per-existing-library state (like an already-saved
// LibraryOptions row) that would give the flag a meaning here anyway.
func (s *Server) handleLibrariesAvailableOptions(w http.ResponseWriter, r *http.Request) {
	contentType := r.URL.Query().Get("LibraryContentType")
	types, ok := libraryContentTypeTypes[contentType]
	if !ok {
		types = libraryContentTypeTypes["mixed"]
	}

	typeOptions := make([]libraryTypeOptions, 0, len(types))
	includesNFOKind := false
	for _, t := range types {
		typeOptions = append(typeOptions, libraryTypeOptionsByType[t])
		if t == "Movie" || t == "Series" || t == "Episode" {
			includesNFOKind = true
		}
	}

	readers := []libraryFetcherInfo{}
	if includesNFOKind {
		readers = []libraryFetcherInfo{nfoReader}
	}

	writeJSON(w, http.StatusOK, libraryAvailableOptions{
		MetadataSavers:        []libraryFetcherInfo{},
		MetadataReaders:       readers,
		SubtitleFetchers:      []libraryFetcherInfo{},
		LyricFetchers:         []libraryFetcherInfo{},
		MediaSegmentProviders: []libraryFetcherInfo{},
		TypeOptions:           typeOptions,
	})
}

// cultures is the language dataset; see package culture.
var cultures = culturepkg.All

// countries is the confirmed live ISO 3166 country dataset (see the package
// doc comment) — 139 entries, matching the reference server's own GET
// /Localization/countries response count exactly.
var countries = []country{
	{Name: "AF", DisplayName: "Afghanistan", TwoLetterISORegionName: "AF", ThreeLetterISORegionName: "AFG"},
	{Name: "AL", DisplayName: "Albania", TwoLetterISORegionName: "AL", ThreeLetterISORegionName: "ALB"},
	{Name: "DZ", DisplayName: "Algeria", TwoLetterISORegionName: "DZ", ThreeLetterISORegionName: "DZA"},
	{Name: "AR", DisplayName: "Argentina", TwoLetterISORegionName: "AR", ThreeLetterISORegionName: "ARG"},
	{Name: "AM", DisplayName: "Armenia", TwoLetterISORegionName: "AM", ThreeLetterISORegionName: "ARM"},
	{Name: "AU", DisplayName: "Australia", TwoLetterISORegionName: "AU", ThreeLetterISORegionName: "AUS"},
	{Name: "AT", DisplayName: "Austria", TwoLetterISORegionName: "AT", ThreeLetterISORegionName: "AUT"},
	{Name: "AZ", DisplayName: "Azerbaijan", TwoLetterISORegionName: "AZ", ThreeLetterISORegionName: "AZE"},
	{Name: "BH", DisplayName: "Bahrain", TwoLetterISORegionName: "BH", ThreeLetterISORegionName: "BHR"},
	{Name: "BD", DisplayName: "Bangladesh", TwoLetterISORegionName: "BD", ThreeLetterISORegionName: "BGD"},
	{Name: "BY", DisplayName: "Belarus", TwoLetterISORegionName: "BY", ThreeLetterISORegionName: "BLR"},
	{Name: "BE", DisplayName: "Belgium", TwoLetterISORegionName: "BE", ThreeLetterISORegionName: "BEL"},
	{Name: "BZ", DisplayName: "Belize", TwoLetterISORegionName: "BZ", ThreeLetterISORegionName: "BLZ"},
	{Name: "VE", DisplayName: "Bolivarian Republic of Venezuela", TwoLetterISORegionName: "VE", ThreeLetterISORegionName: "VEN"},
	{Name: "BO", DisplayName: "Bolivia", TwoLetterISORegionName: "BO", ThreeLetterISORegionName: "BOL"},
	{Name: "BA", DisplayName: "Bosnia and Herzegovina", TwoLetterISORegionName: "BA", ThreeLetterISORegionName: "BIH"},
	{Name: "BW", DisplayName: "Botswana", TwoLetterISORegionName: "BW", ThreeLetterISORegionName: "BWA"},
	{Name: "BR", DisplayName: "Brazil", TwoLetterISORegionName: "BR", ThreeLetterISORegionName: "BRA"},
	{Name: "BN", DisplayName: "Brunei Darussalam", TwoLetterISORegionName: "BN", ThreeLetterISORegionName: "BRN"},
	{Name: "BG", DisplayName: "Bulgaria", TwoLetterISORegionName: "BG", ThreeLetterISORegionName: "BGR"},
	{Name: "KH", DisplayName: "Cambodia", TwoLetterISORegionName: "KH", ThreeLetterISORegionName: "KHM"},
	{Name: "CM", DisplayName: "Cameroon", TwoLetterISORegionName: "CM", ThreeLetterISORegionName: "CMR"},
	{Name: "CA", DisplayName: "Canada", TwoLetterISORegionName: "CA", ThreeLetterISORegionName: "CAN"},
	{Name: "029", DisplayName: "Caribbean", TwoLetterISORegionName: "029", ThreeLetterISORegionName: "029"},
	{Name: "CL", DisplayName: "Chile", TwoLetterISORegionName: "CL", ThreeLetterISORegionName: "CHL"},
	{Name: "CO", DisplayName: "Colombia", TwoLetterISORegionName: "CO", ThreeLetterISORegionName: "COL"},
	{Name: "CD", DisplayName: "Congo [DRC]", TwoLetterISORegionName: "CD", ThreeLetterISORegionName: "COD"},
	{Name: "CR", DisplayName: "Costa Rica", TwoLetterISORegionName: "CR", ThreeLetterISORegionName: "CRI"},
	{Name: "HR", DisplayName: "Croatia", TwoLetterISORegionName: "HR", ThreeLetterISORegionName: "HRV"},
	{Name: "CZ", DisplayName: "Czech Republic", TwoLetterISORegionName: "CZ", ThreeLetterISORegionName: "CZE"},
	{Name: "DK", DisplayName: "Denmark", TwoLetterISORegionName: "DK", ThreeLetterISORegionName: "DNK"},
	{Name: "DO", DisplayName: "Dominican Republic", TwoLetterISORegionName: "DO", ThreeLetterISORegionName: "DOM"},
	{Name: "EC", DisplayName: "Ecuador", TwoLetterISORegionName: "EC", ThreeLetterISORegionName: "ECU"},
	{Name: "EG", DisplayName: "Egypt", TwoLetterISORegionName: "EG", ThreeLetterISORegionName: "EGY"},
	{Name: "SV", DisplayName: "El Salvador", TwoLetterISORegionName: "SV", ThreeLetterISORegionName: "SLV"},
	{Name: "ER", DisplayName: "Eritrea", TwoLetterISORegionName: "ER", ThreeLetterISORegionName: "ERI"},
	{Name: "EE", DisplayName: "Estonia", TwoLetterISORegionName: "EE", ThreeLetterISORegionName: "EST"},
	{Name: "ET", DisplayName: "Ethiopia", TwoLetterISORegionName: "ET", ThreeLetterISORegionName: "ETH"},
	{Name: "FO", DisplayName: "Faroe Islands", TwoLetterISORegionName: "FO", ThreeLetterISORegionName: "FRO"},
	{Name: "FI", DisplayName: "Finland", TwoLetterISORegionName: "FI", ThreeLetterISORegionName: "FIN"},
	{Name: "FR", DisplayName: "France", TwoLetterISORegionName: "FR", ThreeLetterISORegionName: "FRA"},
	{Name: "GE", DisplayName: "Georgia", TwoLetterISORegionName: "GE", ThreeLetterISORegionName: "GEO"},
	{Name: "DE", DisplayName: "Germany", TwoLetterISORegionName: "DE", ThreeLetterISORegionName: "DEU"},
	{Name: "GR", DisplayName: "Greece", TwoLetterISORegionName: "GR", ThreeLetterISORegionName: "GRC"},
	{Name: "GL", DisplayName: "Greenland", TwoLetterISORegionName: "GL", ThreeLetterISORegionName: "GRL"},
	{Name: "GT", DisplayName: "Guatemala", TwoLetterISORegionName: "GT", ThreeLetterISORegionName: "GTM"},
	{Name: "HT", DisplayName: "Haiti", TwoLetterISORegionName: "HT", ThreeLetterISORegionName: "HTI"},
	{Name: "HN", DisplayName: "Honduras", TwoLetterISORegionName: "HN", ThreeLetterISORegionName: "HND"},
	{Name: "HK", DisplayName: "Hong Kong S.A.R.", TwoLetterISORegionName: "HK", ThreeLetterISORegionName: "HKG"},
	{Name: "HU", DisplayName: "Hungary", TwoLetterISORegionName: "HU", ThreeLetterISORegionName: "HUN"},
	{Name: "IS", DisplayName: "Iceland", TwoLetterISORegionName: "IS", ThreeLetterISORegionName: "ISL"},
	{Name: "IN", DisplayName: "India", TwoLetterISORegionName: "IN", ThreeLetterISORegionName: "IND"},
	{Name: "ID", DisplayName: "Indonesia", TwoLetterISORegionName: "ID", ThreeLetterISORegionName: "IDN"},
	{Name: "IR", DisplayName: "Iran", TwoLetterISORegionName: "IR", ThreeLetterISORegionName: "IRN"},
	{Name: "IQ", DisplayName: "Iraq", TwoLetterISORegionName: "IQ", ThreeLetterISORegionName: "IRQ"},
	{Name: "IE", DisplayName: "Ireland", TwoLetterISORegionName: "IE", ThreeLetterISORegionName: "IRL"},
	{Name: "PK", DisplayName: "Pakistan", TwoLetterISORegionName: "PK", ThreeLetterISORegionName: "PAK"},
	{Name: "IL", DisplayName: "Israel", TwoLetterISORegionName: "IL", ThreeLetterISORegionName: "ISR"},
	{Name: "IT", DisplayName: "Italy", TwoLetterISORegionName: "IT", ThreeLetterISORegionName: "ITA"},
	{Name: "CI", DisplayName: "Ivory Coast", TwoLetterISORegionName: "CI", ThreeLetterISORegionName: "CIV"},
	{Name: "JM", DisplayName: "Jamaica", TwoLetterISORegionName: "JM", ThreeLetterISORegionName: "JAM"},
	{Name: "JP", DisplayName: "Japan", TwoLetterISORegionName: "JP", ThreeLetterISORegionName: "JPN"},
	{Name: "JO", DisplayName: "Jordan", TwoLetterISORegionName: "JO", ThreeLetterISORegionName: "JOR"},
	{Name: "KZ", DisplayName: "Kazakhstan", TwoLetterISORegionName: "KZ", ThreeLetterISORegionName: "KAZ"},
	{Name: "KE", DisplayName: "Kenya", TwoLetterISORegionName: "KE", ThreeLetterISORegionName: "KEN"},
	{Name: "KR", DisplayName: "Korea", TwoLetterISORegionName: "KR", ThreeLetterISORegionName: "KOR"},
	{Name: "KW", DisplayName: "Kuwait", TwoLetterISORegionName: "KW", ThreeLetterISORegionName: "KWT"},
	{Name: "KG", DisplayName: "Kyrgyzstan", TwoLetterISORegionName: "KG", ThreeLetterISORegionName: "KGZ"},
	{Name: "LA", DisplayName: "Lao P.D.R.", TwoLetterISORegionName: "LA", ThreeLetterISORegionName: "LAO"},
	{Name: "419", DisplayName: "Latin America", TwoLetterISORegionName: "419", ThreeLetterISORegionName: "419"},
	{Name: "LV", DisplayName: "Latvia", TwoLetterISORegionName: "LV", ThreeLetterISORegionName: "LVA"},
	{Name: "LB", DisplayName: "Lebanon", TwoLetterISORegionName: "LB", ThreeLetterISORegionName: "LBN"},
	{Name: "LY", DisplayName: "Libya", TwoLetterISORegionName: "LY", ThreeLetterISORegionName: "LBY"},
	{Name: "LI", DisplayName: "Liechtenstein", TwoLetterISORegionName: "LI", ThreeLetterISORegionName: "LIE"},
	{Name: "LT", DisplayName: "Lithuania", TwoLetterISORegionName: "LT", ThreeLetterISORegionName: "LTU"},
	{Name: "LU", DisplayName: "Luxembourg", TwoLetterISORegionName: "LU", ThreeLetterISORegionName: "LUX"},
	{Name: "MO", DisplayName: "Macao S.A.R.", TwoLetterISORegionName: "MO", ThreeLetterISORegionName: "MAC"},
	{Name: "MK", DisplayName: "Macedonia (FYROM)", TwoLetterISORegionName: "MK", ThreeLetterISORegionName: "MKD"},
	{Name: "MY", DisplayName: "Malaysia", TwoLetterISORegionName: "MY", ThreeLetterISORegionName: "MYS"},
	{Name: "MV", DisplayName: "Maldives", TwoLetterISORegionName: "MV", ThreeLetterISORegionName: "MDV"},
	{Name: "ML", DisplayName: "Mali", TwoLetterISORegionName: "ML", ThreeLetterISORegionName: "MLI"},
	{Name: "MT", DisplayName: "Malta", TwoLetterISORegionName: "MT", ThreeLetterISORegionName: "MLT"},
	{Name: "MX", DisplayName: "Mexico", TwoLetterISORegionName: "MX", ThreeLetterISORegionName: "MEX"},
	{Name: "MN", DisplayName: "Mongolia", TwoLetterISORegionName: "MN", ThreeLetterISORegionName: "MNG"},
	{Name: "ME", DisplayName: "Montenegro", TwoLetterISORegionName: "ME", ThreeLetterISORegionName: "MNE"},
	{Name: "MA", DisplayName: "Morocco", TwoLetterISORegionName: "MA", ThreeLetterISORegionName: "MAR"},
	{Name: "NP", DisplayName: "Nepal", TwoLetterISORegionName: "NP", ThreeLetterISORegionName: "NPL"},
	{Name: "NL", DisplayName: "Netherlands", TwoLetterISORegionName: "NL", ThreeLetterISORegionName: "NLD"},
	{Name: "NZ", DisplayName: "New Zealand", TwoLetterISORegionName: "NZ", ThreeLetterISORegionName: "NZL"},
	{Name: "NI", DisplayName: "Nicaragua", TwoLetterISORegionName: "NI", ThreeLetterISORegionName: "NIC"},
	{Name: "NG", DisplayName: "Nigeria", TwoLetterISORegionName: "NG", ThreeLetterISORegionName: "NGA"},
	{Name: "NO", DisplayName: "Norway", TwoLetterISORegionName: "NO", ThreeLetterISORegionName: "NOR"},
	{Name: "OM", DisplayName: "Oman", TwoLetterISORegionName: "OM", ThreeLetterISORegionName: "OMN"},
	{Name: "PS", DisplayName: "Palestine", TwoLetterISORegionName: "PS", ThreeLetterISORegionName: "PSE"},
	{Name: "PA", DisplayName: "Panama", TwoLetterISORegionName: "PA", ThreeLetterISORegionName: "PAN"},
	{Name: "PY", DisplayName: "Paraguay", TwoLetterISORegionName: "PY", ThreeLetterISORegionName: "PRY"},
	{Name: "CN", DisplayName: "People's Republic of China", TwoLetterISORegionName: "CN", ThreeLetterISORegionName: "CHN"},
	{Name: "PE", DisplayName: "Peru", TwoLetterISORegionName: "PE", ThreeLetterISORegionName: "PER"},
	{Name: "PH", DisplayName: "Philippines", TwoLetterISORegionName: "PH", ThreeLetterISORegionName: "PHL"},
	{Name: "PL", DisplayName: "Poland", TwoLetterISORegionName: "PL", ThreeLetterISORegionName: "POL"},
	{Name: "PT", DisplayName: "Portugal", TwoLetterISORegionName: "PT", ThreeLetterISORegionName: "PRT"},
	{Name: "MC", DisplayName: "Principality of Monaco", TwoLetterISORegionName: "MC", ThreeLetterISORegionName: "MCO"},
	{Name: "PR", DisplayName: "Puerto Rico", TwoLetterISORegionName: "PR", ThreeLetterISORegionName: "PRI"},
	{Name: "QA", DisplayName: "Qatar", TwoLetterISORegionName: "QA", ThreeLetterISORegionName: "QAT"},
	{Name: "MD", DisplayName: "Republica Moldova", TwoLetterISORegionName: "MD", ThreeLetterISORegionName: "MDA"},
	{Name: "RE", DisplayName: "Réunion", TwoLetterISORegionName: "RE", ThreeLetterISORegionName: "REU"},
	{Name: "RO", DisplayName: "Romania", TwoLetterISORegionName: "RO", ThreeLetterISORegionName: "ROU"},
	{Name: "RU", DisplayName: "Russia", TwoLetterISORegionName: "RU", ThreeLetterISORegionName: "RUS"},
	{Name: "RW", DisplayName: "Rwanda", TwoLetterISORegionName: "RW", ThreeLetterISORegionName: "RWA"},
	{Name: "SA", DisplayName: "Saudi Arabia", TwoLetterISORegionName: "SA", ThreeLetterISORegionName: "SAU"},
	{Name: "SN", DisplayName: "Senegal", TwoLetterISORegionName: "SN", ThreeLetterISORegionName: "SEN"},
	{Name: "RS", DisplayName: "Serbia", TwoLetterISORegionName: "RS", ThreeLetterISORegionName: "SRB"},
	{Name: "CS", DisplayName: "Serbia and Montenegro (Former)", TwoLetterISORegionName: "CS", ThreeLetterISORegionName: "SCG"},
	{Name: "SG", DisplayName: "Singapore", TwoLetterISORegionName: "SG", ThreeLetterISORegionName: "SGP"},
	{Name: "SK", DisplayName: "Slovakia", TwoLetterISORegionName: "SK", ThreeLetterISORegionName: "SVK"},
	{Name: "SI", DisplayName: "Slovenia", TwoLetterISORegionName: "SI", ThreeLetterISORegionName: "SVN"},
	{Name: "SO", DisplayName: "Somalia", TwoLetterISORegionName: "SO", ThreeLetterISORegionName: "SOM"},
	{Name: "ZA", DisplayName: "South Africa", TwoLetterISORegionName: "ZA", ThreeLetterISORegionName: "ZAF"},
	{Name: "ES", DisplayName: "Spain", TwoLetterISORegionName: "ES", ThreeLetterISORegionName: "ESP"},
	{Name: "LK", DisplayName: "Sri Lanka", TwoLetterISORegionName: "LK", ThreeLetterISORegionName: "LKA"},
	{Name: "SE", DisplayName: "Sweden", TwoLetterISORegionName: "SE", ThreeLetterISORegionName: "SWE"},
	{Name: "CH", DisplayName: "Switzerland", TwoLetterISORegionName: "CH", ThreeLetterISORegionName: "CHE"},
	{Name: "SY", DisplayName: "Syria", TwoLetterISORegionName: "SY", ThreeLetterISORegionName: "SYR"},
	{Name: "TW", DisplayName: "Taiwan", TwoLetterISORegionName: "TW", ThreeLetterISORegionName: "TWN"},
	{Name: "TJ", DisplayName: "Tajikistan", TwoLetterISORegionName: "TJ", ThreeLetterISORegionName: "TAJ"},
	{Name: "TH", DisplayName: "Thailand", TwoLetterISORegionName: "TH", ThreeLetterISORegionName: "THA"},
	{Name: "TT", DisplayName: "Trinidad and Tobago", TwoLetterISORegionName: "TT", ThreeLetterISORegionName: "TTO"},
	{Name: "TN", DisplayName: "Tunisia", TwoLetterISORegionName: "TN", ThreeLetterISORegionName: "TUN"},
	{Name: "TR", DisplayName: "Turkey", TwoLetterISORegionName: "TR", ThreeLetterISORegionName: "TUR"},
	{Name: "TM", DisplayName: "Turkmenistan", TwoLetterISORegionName: "TM", ThreeLetterISORegionName: "TKM"},
	{Name: "AE", DisplayName: "U.A.E.", TwoLetterISORegionName: "AE", ThreeLetterISORegionName: "ARE"},
	{Name: "UA", DisplayName: "Ukraine", TwoLetterISORegionName: "UA", ThreeLetterISORegionName: "UKR"},
	{Name: "GB", DisplayName: "United Kingdom", TwoLetterISORegionName: "GB", ThreeLetterISORegionName: "GBR"},
	{Name: "US", DisplayName: "United States", TwoLetterISORegionName: "US", ThreeLetterISORegionName: "USA"},
	{Name: "UY", DisplayName: "Uruguay", TwoLetterISORegionName: "UY", ThreeLetterISORegionName: "URY"},
	{Name: "UZ", DisplayName: "Uzbekistan", TwoLetterISORegionName: "UZ", ThreeLetterISORegionName: "UZB"},
	{Name: "VN", DisplayName: "Vietnam", TwoLetterISORegionName: "VN", ThreeLetterISORegionName: "VNM"},
	{Name: "YE", DisplayName: "Yemen", TwoLetterISORegionName: "YE", ThreeLetterISORegionName: "YEM"},
	{Name: "ZW", DisplayName: "Zimbabwe", TwoLetterISORegionName: "ZW", ThreeLetterISORegionName: "ZWE"}}
