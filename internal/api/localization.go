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
)

type culture struct {
	Name                       string `json:"Name"`
	DisplayName                string `json:"DisplayName"`
	TwoLetterISOLanguageName   string `json:"TwoLetterISOLanguageName"`
	ThreeLetterISOLanguageName string `json:"ThreeLetterISOLanguageName"`
	// ThreeLetterISOLanguageNames always carries at least
	// ThreeLetterISOLanguageName itself; a second entry appears only for
	// the handful of languages ISO 639-2 gives both a "terminological" and
	// a legacy "bibliographic" code (e.g. German: deu/ger) — confirmed
	// live, not guessed.
	ThreeLetterISOLanguageNames []string `json:"ThreeLetterISOLanguageNames"`
}

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

// cultures is the confirmed live ISO 639-1/639-2 language dataset (see the
// package doc comment) — 192 entries, matching the reference server's own
// GET /Localization/cultures response count exactly.
var cultures = []culture{
	{Name: "Abkhazian", DisplayName: "Abkhazian", TwoLetterISOLanguageName: "ab", ThreeLetterISOLanguageName: "abk", ThreeLetterISOLanguageNames: []string{"abk"}},
	{Name: "Afar", DisplayName: "Afar", TwoLetterISOLanguageName: "aa", ThreeLetterISOLanguageName: "aar", ThreeLetterISOLanguageNames: []string{"aar"}},
	{Name: "Afrikaans", DisplayName: "Afrikaans", TwoLetterISOLanguageName: "af", ThreeLetterISOLanguageName: "afr", ThreeLetterISOLanguageNames: []string{"afr"}},
	{Name: "Akan", DisplayName: "Akan", TwoLetterISOLanguageName: "ak", ThreeLetterISOLanguageName: "aka", ThreeLetterISOLanguageNames: []string{"aka"}},
	{Name: "Albanian", DisplayName: "Albanian", TwoLetterISOLanguageName: "sq", ThreeLetterISOLanguageName: "sqi", ThreeLetterISOLanguageNames: []string{"sqi", "alb"}},
	{Name: "Amharic", DisplayName: "Amharic", TwoLetterISOLanguageName: "am", ThreeLetterISOLanguageName: "amh", ThreeLetterISOLanguageNames: []string{"amh"}},
	{Name: "Arabic", DisplayName: "Arabic", TwoLetterISOLanguageName: "ar", ThreeLetterISOLanguageName: "ara", ThreeLetterISOLanguageNames: []string{"ara"}},
	{Name: "Aragonese", DisplayName: "Aragonese", TwoLetterISOLanguageName: "an", ThreeLetterISOLanguageName: "arg", ThreeLetterISOLanguageNames: []string{"arg"}},
	{Name: "Armenian", DisplayName: "Armenian", TwoLetterISOLanguageName: "hy", ThreeLetterISOLanguageName: "hye", ThreeLetterISOLanguageNames: []string{"hye", "arm"}},
	{Name: "Assamese", DisplayName: "Assamese", TwoLetterISOLanguageName: "as", ThreeLetterISOLanguageName: "asm", ThreeLetterISOLanguageNames: []string{"asm"}},
	{Name: "Avaric", DisplayName: "Avaric", TwoLetterISOLanguageName: "av", ThreeLetterISOLanguageName: "ava", ThreeLetterISOLanguageNames: []string{"ava"}},
	{Name: "Avestan", DisplayName: "Avestan", TwoLetterISOLanguageName: "ae", ThreeLetterISOLanguageName: "ave", ThreeLetterISOLanguageNames: []string{"ave"}},
	{Name: "Aymara", DisplayName: "Aymara", TwoLetterISOLanguageName: "ay", ThreeLetterISOLanguageName: "aym", ThreeLetterISOLanguageNames: []string{"aym"}},
	{Name: "Azerbaijani", DisplayName: "Azerbaijani", TwoLetterISOLanguageName: "az", ThreeLetterISOLanguageName: "aze", ThreeLetterISOLanguageNames: []string{"aze"}},
	{Name: "Bambara", DisplayName: "Bambara", TwoLetterISOLanguageName: "bm", ThreeLetterISOLanguageName: "bam", ThreeLetterISOLanguageNames: []string{"bam"}},
	{Name: "Bashkir", DisplayName: "Bashkir", TwoLetterISOLanguageName: "ba", ThreeLetterISOLanguageName: "bak", ThreeLetterISOLanguageNames: []string{"bak"}},
	{Name: "Basque", DisplayName: "Basque", TwoLetterISOLanguageName: "eu", ThreeLetterISOLanguageName: "eus", ThreeLetterISOLanguageNames: []string{"eus", "baq"}},
	{Name: "Belarusian", DisplayName: "Belarusian", TwoLetterISOLanguageName: "be", ThreeLetterISOLanguageName: "bel", ThreeLetterISOLanguageNames: []string{"bel"}},
	{Name: "Bengali", DisplayName: "Bengali", TwoLetterISOLanguageName: "bn", ThreeLetterISOLanguageName: "ben", ThreeLetterISOLanguageNames: []string{"ben"}},
	{Name: "Bihari languages", DisplayName: "Bihari languages", TwoLetterISOLanguageName: "bh", ThreeLetterISOLanguageName: "bih", ThreeLetterISOLanguageNames: []string{"bih"}},
	{Name: "Bislama", DisplayName: "Bislama", TwoLetterISOLanguageName: "bi", ThreeLetterISOLanguageName: "bis", ThreeLetterISOLanguageNames: []string{"bis"}},
	{Name: "Bosnian", DisplayName: "Bosnian", TwoLetterISOLanguageName: "bs", ThreeLetterISOLanguageName: "bos", ThreeLetterISOLanguageNames: []string{"bos"}},
	{Name: "Breton", DisplayName: "Breton", TwoLetterISOLanguageName: "br", ThreeLetterISOLanguageName: "bre", ThreeLetterISOLanguageNames: []string{"bre"}},
	{Name: "Bulgarian", DisplayName: "Bulgarian", TwoLetterISOLanguageName: "bg", ThreeLetterISOLanguageName: "bul", ThreeLetterISOLanguageNames: []string{"bul"}},
	{Name: "Burmese", DisplayName: "Burmese", TwoLetterISOLanguageName: "my", ThreeLetterISOLanguageName: "mya", ThreeLetterISOLanguageNames: []string{"mya", "bur"}},
	{Name: "Catalan; Valencian", DisplayName: "Catalan; Valencian", TwoLetterISOLanguageName: "ca", ThreeLetterISOLanguageName: "cat", ThreeLetterISOLanguageNames: []string{"cat"}},
	{Name: "Central Khmer", DisplayName: "Central Khmer", TwoLetterISOLanguageName: "km", ThreeLetterISOLanguageName: "khm", ThreeLetterISOLanguageNames: []string{"khm"}},
	{Name: "Chamorro", DisplayName: "Chamorro", TwoLetterISOLanguageName: "ch", ThreeLetterISOLanguageName: "cha", ThreeLetterISOLanguageNames: []string{"cha"}},
	{Name: "Chechen", DisplayName: "Chechen", TwoLetterISOLanguageName: "ce", ThreeLetterISOLanguageName: "che", ThreeLetterISOLanguageNames: []string{"che"}},
	{Name: "Chichewa; Chewa; Nyanja", DisplayName: "Chichewa; Chewa; Nyanja", TwoLetterISOLanguageName: "ny", ThreeLetterISOLanguageName: "nya", ThreeLetterISOLanguageNames: []string{"nya"}},
	{Name: "Chinese", DisplayName: "Chinese", TwoLetterISOLanguageName: "zh", ThreeLetterISOLanguageName: "zho", ThreeLetterISOLanguageNames: []string{"zho", "chi"}},
	{Name: "Chinese (Bilingual)", DisplayName: "Chinese (Bilingual)", TwoLetterISOLanguageName: "ze", ThreeLetterISOLanguageName: "zho", ThreeLetterISOLanguageNames: []string{"zho", "chi"}},
	{Name: "zh-hk", DisplayName: "Chinese (Hong Kong)", TwoLetterISOLanguageName: "zh-hk", ThreeLetterISOLanguageName: "zho", ThreeLetterISOLanguageNames: []string{"zho", "chi"}},
	{Name: "zh-cn", DisplayName: "Chinese (Simplified)", TwoLetterISOLanguageName: "zh-cn", ThreeLetterISOLanguageName: "zho", ThreeLetterISOLanguageNames: []string{"zho", "chi"}},
	{Name: "zh-tw", DisplayName: "Chinese (Traditional)", TwoLetterISOLanguageName: "zh-tw", ThreeLetterISOLanguageName: "zho", ThreeLetterISOLanguageNames: []string{"zho", "chi"}},
	{Name: "Church Slavic; Old Slavonic; Church Slavonic; Old Bulgarian; Old Church Slavonic", DisplayName: "Church Slavic; Old Slavonic; Church Slavonic; Old Bulgarian; Old Church Slavonic", TwoLetterISOLanguageName: "cu", ThreeLetterISOLanguageName: "chu", ThreeLetterISOLanguageNames: []string{"chu"}},
	{Name: "Chuvash", DisplayName: "Chuvash", TwoLetterISOLanguageName: "cv", ThreeLetterISOLanguageName: "chv", ThreeLetterISOLanguageNames: []string{"chv"}},
	{Name: "Cornish", DisplayName: "Cornish", TwoLetterISOLanguageName: "kw", ThreeLetterISOLanguageName: "cor", ThreeLetterISOLanguageNames: []string{"cor"}},
	{Name: "Corsican", DisplayName: "Corsican", TwoLetterISOLanguageName: "co", ThreeLetterISOLanguageName: "cos", ThreeLetterISOLanguageNames: []string{"cos"}},
	{Name: "Cree", DisplayName: "Cree", TwoLetterISOLanguageName: "cr", ThreeLetterISOLanguageName: "cre", ThreeLetterISOLanguageNames: []string{"cre"}},
	{Name: "Croatian", DisplayName: "Croatian", TwoLetterISOLanguageName: "hr", ThreeLetterISOLanguageName: "hrv", ThreeLetterISOLanguageNames: []string{"hrv"}},
	{Name: "Czech", DisplayName: "Czech", TwoLetterISOLanguageName: "cs", ThreeLetterISOLanguageName: "ces", ThreeLetterISOLanguageNames: []string{"ces", "cze"}},
	{Name: "Danish", DisplayName: "Danish", TwoLetterISOLanguageName: "da", ThreeLetterISOLanguageName: "dan", ThreeLetterISOLanguageNames: []string{"dan"}},
	{Name: "Divehi; Dhivehi; Maldivian", DisplayName: "Divehi; Dhivehi; Maldivian", TwoLetterISOLanguageName: "dv", ThreeLetterISOLanguageName: "div", ThreeLetterISOLanguageNames: []string{"div"}},
	{Name: "Dutch; Flemish", DisplayName: "Dutch; Flemish", TwoLetterISOLanguageName: "nl", ThreeLetterISOLanguageName: "nld", ThreeLetterISOLanguageNames: []string{"nld", "dut"}},
	{Name: "Dzongkha", DisplayName: "Dzongkha", TwoLetterISOLanguageName: "dz", ThreeLetterISOLanguageName: "dzo", ThreeLetterISOLanguageNames: []string{"dzo"}},
	{Name: "English", DisplayName: "English", TwoLetterISOLanguageName: "en", ThreeLetterISOLanguageName: "eng", ThreeLetterISOLanguageNames: []string{"eng"}},
	{Name: "Esperanto", DisplayName: "Esperanto", TwoLetterISOLanguageName: "eo", ThreeLetterISOLanguageName: "epo", ThreeLetterISOLanguageNames: []string{"epo"}},
	{Name: "Estonian", DisplayName: "Estonian", TwoLetterISOLanguageName: "et", ThreeLetterISOLanguageName: "est", ThreeLetterISOLanguageNames: []string{"est"}},
	{Name: "Ewe", DisplayName: "Ewe", TwoLetterISOLanguageName: "ee", ThreeLetterISOLanguageName: "ewe", ThreeLetterISOLanguageNames: []string{"ewe"}},
	{Name: "Faroese", DisplayName: "Faroese", TwoLetterISOLanguageName: "fo", ThreeLetterISOLanguageName: "fao", ThreeLetterISOLanguageNames: []string{"fao"}},
	{Name: "Fijian", DisplayName: "Fijian", TwoLetterISOLanguageName: "fj", ThreeLetterISOLanguageName: "fij", ThreeLetterISOLanguageNames: []string{"fij"}},
	{Name: "Finnish", DisplayName: "Finnish", TwoLetterISOLanguageName: "fi", ThreeLetterISOLanguageName: "fin", ThreeLetterISOLanguageNames: []string{"fin"}},
	{Name: "French", DisplayName: "French", TwoLetterISOLanguageName: "fr", ThreeLetterISOLanguageName: "fra", ThreeLetterISOLanguageNames: []string{"fra", "fre"}},
	{Name: "fr-ca", DisplayName: "French (Canada)", TwoLetterISOLanguageName: "fr-ca", ThreeLetterISOLanguageName: "frc", ThreeLetterISOLanguageNames: []string{"frc"}},
	{Name: "Fulah", DisplayName: "Fulah", TwoLetterISOLanguageName: "ff", ThreeLetterISOLanguageName: "ful", ThreeLetterISOLanguageNames: []string{"ful"}},
	{Name: "Gaelic; Scottish Gaelic", DisplayName: "Gaelic; Scottish Gaelic", TwoLetterISOLanguageName: "gd", ThreeLetterISOLanguageName: "gla", ThreeLetterISOLanguageNames: []string{"gla"}},
	{Name: "Galician", DisplayName: "Galician", TwoLetterISOLanguageName: "gl", ThreeLetterISOLanguageName: "glg", ThreeLetterISOLanguageNames: []string{"glg"}},
	{Name: "Ganda", DisplayName: "Ganda", TwoLetterISOLanguageName: "lg", ThreeLetterISOLanguageName: "lug", ThreeLetterISOLanguageNames: []string{"lug"}},
	{Name: "Georgian", DisplayName: "Georgian", TwoLetterISOLanguageName: "ka", ThreeLetterISOLanguageName: "kat", ThreeLetterISOLanguageNames: []string{"kat", "geo"}},
	{Name: "German", DisplayName: "German", TwoLetterISOLanguageName: "de", ThreeLetterISOLanguageName: "deu", ThreeLetterISOLanguageNames: []string{"deu", "ger"}},
	{Name: "Greek, Modern (1453-)", DisplayName: "Greek, Modern (1453-)", TwoLetterISOLanguageName: "el", ThreeLetterISOLanguageName: "ell", ThreeLetterISOLanguageNames: []string{"ell", "gre"}},
	{Name: "Guarani", DisplayName: "Guarani", TwoLetterISOLanguageName: "gn", ThreeLetterISOLanguageName: "grn", ThreeLetterISOLanguageNames: []string{"grn"}},
	{Name: "Gujarati", DisplayName: "Gujarati", TwoLetterISOLanguageName: "gu", ThreeLetterISOLanguageName: "guj", ThreeLetterISOLanguageNames: []string{"guj"}},
	{Name: "Haitian; Haitian Creole", DisplayName: "Haitian; Haitian Creole", TwoLetterISOLanguageName: "ht", ThreeLetterISOLanguageName: "hat", ThreeLetterISOLanguageNames: []string{"hat"}},
	{Name: "Hausa", DisplayName: "Hausa", TwoLetterISOLanguageName: "ha", ThreeLetterISOLanguageName: "hau", ThreeLetterISOLanguageNames: []string{"hau"}},
	{Name: "Hebrew", DisplayName: "Hebrew", TwoLetterISOLanguageName: "he", ThreeLetterISOLanguageName: "heb", ThreeLetterISOLanguageNames: []string{"heb"}},
	{Name: "Herero", DisplayName: "Herero", TwoLetterISOLanguageName: "hz", ThreeLetterISOLanguageName: "her", ThreeLetterISOLanguageNames: []string{"her"}},
	{Name: "Hindi", DisplayName: "Hindi", TwoLetterISOLanguageName: "hi", ThreeLetterISOLanguageName: "hin", ThreeLetterISOLanguageNames: []string{"hin"}},
	{Name: "Hiri Motu", DisplayName: "Hiri Motu", TwoLetterISOLanguageName: "ho", ThreeLetterISOLanguageName: "hmo", ThreeLetterISOLanguageNames: []string{"hmo"}},
	{Name: "Hungarian", DisplayName: "Hungarian", TwoLetterISOLanguageName: "hu", ThreeLetterISOLanguageName: "hun", ThreeLetterISOLanguageNames: []string{"hun"}},
	{Name: "Icelandic", DisplayName: "Icelandic", TwoLetterISOLanguageName: "is", ThreeLetterISOLanguageName: "isl", ThreeLetterISOLanguageNames: []string{"isl", "ice"}},
	{Name: "Ido", DisplayName: "Ido", TwoLetterISOLanguageName: "io", ThreeLetterISOLanguageName: "ido", ThreeLetterISOLanguageNames: []string{"ido"}},
	{Name: "Igbo", DisplayName: "Igbo", TwoLetterISOLanguageName: "ig", ThreeLetterISOLanguageName: "ibo", ThreeLetterISOLanguageNames: []string{"ibo"}},
	{Name: "Indonesian", DisplayName: "Indonesian", TwoLetterISOLanguageName: "id", ThreeLetterISOLanguageName: "ind", ThreeLetterISOLanguageNames: []string{"ind"}},
	{Name: "Interlingua (International Auxiliary Language Association)", DisplayName: "Interlingua (International Auxiliary Language Association)", TwoLetterISOLanguageName: "ia", ThreeLetterISOLanguageName: "ina", ThreeLetterISOLanguageNames: []string{"ina"}},
	{Name: "Interlingue; Occidental", DisplayName: "Interlingue; Occidental", TwoLetterISOLanguageName: "ie", ThreeLetterISOLanguageName: "ile", ThreeLetterISOLanguageNames: []string{"ile"}},
	{Name: "Inuktitut", DisplayName: "Inuktitut", TwoLetterISOLanguageName: "iu", ThreeLetterISOLanguageName: "iku", ThreeLetterISOLanguageNames: []string{"iku"}},
	{Name: "Inupiaq", DisplayName: "Inupiaq", TwoLetterISOLanguageName: "ik", ThreeLetterISOLanguageName: "ipk", ThreeLetterISOLanguageNames: []string{"ipk"}},
	{Name: "Irish", DisplayName: "Irish", TwoLetterISOLanguageName: "ga", ThreeLetterISOLanguageName: "gle", ThreeLetterISOLanguageNames: []string{"gle"}},
	{Name: "Italian", DisplayName: "Italian", TwoLetterISOLanguageName: "it", ThreeLetterISOLanguageName: "ita", ThreeLetterISOLanguageNames: []string{"ita"}},
	{Name: "Japanese", DisplayName: "Japanese", TwoLetterISOLanguageName: "ja", ThreeLetterISOLanguageName: "jpn", ThreeLetterISOLanguageNames: []string{"jpn"}},
	{Name: "Javanese", DisplayName: "Javanese", TwoLetterISOLanguageName: "jv", ThreeLetterISOLanguageName: "jav", ThreeLetterISOLanguageNames: []string{"jav"}},
	{Name: "Kalaallisut; Greenlandic", DisplayName: "Kalaallisut; Greenlandic", TwoLetterISOLanguageName: "kl", ThreeLetterISOLanguageName: "kal", ThreeLetterISOLanguageNames: []string{"kal"}},
	{Name: "Kannada", DisplayName: "Kannada", TwoLetterISOLanguageName: "kn", ThreeLetterISOLanguageName: "kan", ThreeLetterISOLanguageNames: []string{"kan"}},
	{Name: "Kanuri", DisplayName: "Kanuri", TwoLetterISOLanguageName: "kr", ThreeLetterISOLanguageName: "kau", ThreeLetterISOLanguageNames: []string{"kau"}},
	{Name: "Kashmiri", DisplayName: "Kashmiri", TwoLetterISOLanguageName: "ks", ThreeLetterISOLanguageName: "kas", ThreeLetterISOLanguageNames: []string{"kas"}},
	{Name: "Kazakh", DisplayName: "Kazakh", TwoLetterISOLanguageName: "kk", ThreeLetterISOLanguageName: "kaz", ThreeLetterISOLanguageNames: []string{"kaz"}},
	{Name: "Kikuyu; Gikuyu", DisplayName: "Kikuyu; Gikuyu", TwoLetterISOLanguageName: "ki", ThreeLetterISOLanguageName: "kik", ThreeLetterISOLanguageNames: []string{"kik"}},
	{Name: "Kinyarwanda", DisplayName: "Kinyarwanda", TwoLetterISOLanguageName: "rw", ThreeLetterISOLanguageName: "kin", ThreeLetterISOLanguageNames: []string{"kin"}},
	{Name: "Kirghiz; Kyrgyz", DisplayName: "Kirghiz; Kyrgyz", TwoLetterISOLanguageName: "ky", ThreeLetterISOLanguageName: "kir", ThreeLetterISOLanguageNames: []string{"kir"}},
	{Name: "Komi", DisplayName: "Komi", TwoLetterISOLanguageName: "kv", ThreeLetterISOLanguageName: "kom", ThreeLetterISOLanguageNames: []string{"kom"}},
	{Name: "Kongo", DisplayName: "Kongo", TwoLetterISOLanguageName: "kg", ThreeLetterISOLanguageName: "kon", ThreeLetterISOLanguageNames: []string{"kon"}},
	{Name: "Korean", DisplayName: "Korean", TwoLetterISOLanguageName: "ko", ThreeLetterISOLanguageName: "kor", ThreeLetterISOLanguageNames: []string{"kor"}},
	{Name: "Kuanyama; Kwanyama", DisplayName: "Kuanyama; Kwanyama", TwoLetterISOLanguageName: "kj", ThreeLetterISOLanguageName: "kua", ThreeLetterISOLanguageNames: []string{"kua"}},
	{Name: "Kurdish", DisplayName: "Kurdish", TwoLetterISOLanguageName: "ku", ThreeLetterISOLanguageName: "kur", ThreeLetterISOLanguageNames: []string{"kur"}},
	{Name: "Lao", DisplayName: "Lao", TwoLetterISOLanguageName: "lo", ThreeLetterISOLanguageName: "lao", ThreeLetterISOLanguageNames: []string{"lao"}},
	{Name: "Latin", DisplayName: "Latin", TwoLetterISOLanguageName: "la", ThreeLetterISOLanguageName: "lat", ThreeLetterISOLanguageNames: []string{"lat"}},
	{Name: "Latvian", DisplayName: "Latvian", TwoLetterISOLanguageName: "lv", ThreeLetterISOLanguageName: "lav", ThreeLetterISOLanguageNames: []string{"lav"}},
	{Name: "Limburgan; Limburger; Limburgish", DisplayName: "Limburgan; Limburger; Limburgish", TwoLetterISOLanguageName: "li", ThreeLetterISOLanguageName: "lim", ThreeLetterISOLanguageNames: []string{"lim"}},
	{Name: "Lingala", DisplayName: "Lingala", TwoLetterISOLanguageName: "ln", ThreeLetterISOLanguageName: "lin", ThreeLetterISOLanguageNames: []string{"lin"}},
	{Name: "Lithuanian", DisplayName: "Lithuanian", TwoLetterISOLanguageName: "lt", ThreeLetterISOLanguageName: "lit", ThreeLetterISOLanguageNames: []string{"lit"}},
	{Name: "Luba-Katanga", DisplayName: "Luba-Katanga", TwoLetterISOLanguageName: "lu", ThreeLetterISOLanguageName: "lub", ThreeLetterISOLanguageNames: []string{"lub"}},
	{Name: "Luxembourgish; Letzeburgesch", DisplayName: "Luxembourgish; Letzeburgesch", TwoLetterISOLanguageName: "lb", ThreeLetterISOLanguageName: "ltz", ThreeLetterISOLanguageNames: []string{"ltz"}},
	{Name: "Macedonian", DisplayName: "Macedonian", TwoLetterISOLanguageName: "omega", ThreeLetterISOLanguageName: "mkd", ThreeLetterISOLanguageNames: []string{"mkd", "mac"}},
	{Name: "Malagasy", DisplayName: "Malagasy", TwoLetterISOLanguageName: "mg", ThreeLetterISOLanguageName: "mlg", ThreeLetterISOLanguageNames: []string{"mlg"}},
	{Name: "Malay", DisplayName: "Malay", TwoLetterISOLanguageName: "ms", ThreeLetterISOLanguageName: "msa", ThreeLetterISOLanguageNames: []string{"msa", "may"}},
	{Name: "Malayalam", DisplayName: "Malayalam", TwoLetterISOLanguageName: "ml", ThreeLetterISOLanguageName: "mal", ThreeLetterISOLanguageNames: []string{"mal"}},
	{Name: "Maltese", DisplayName: "Maltese", TwoLetterISOLanguageName: "mt", ThreeLetterISOLanguageName: "mlt", ThreeLetterISOLanguageNames: []string{"mlt"}},
	{Name: "Manx", DisplayName: "Manx", TwoLetterISOLanguageName: "gv", ThreeLetterISOLanguageName: "glv", ThreeLetterISOLanguageNames: []string{"glv"}},
	{Name: "Maori", DisplayName: "Maori", TwoLetterISOLanguageName: "mi", ThreeLetterISOLanguageName: "mri", ThreeLetterISOLanguageNames: []string{"mri", "mao"}},
	{Name: "Marathi", DisplayName: "Marathi", TwoLetterISOLanguageName: "mr", ThreeLetterISOLanguageName: "mar", ThreeLetterISOLanguageNames: []string{"mar"}},
	{Name: "Marshallese", DisplayName: "Marshallese", TwoLetterISOLanguageName: "mh", ThreeLetterISOLanguageName: "mah", ThreeLetterISOLanguageNames: []string{"mah"}},
	{Name: "Mongolian", DisplayName: "Mongolian", TwoLetterISOLanguageName: "mn", ThreeLetterISOLanguageName: "mon", ThreeLetterISOLanguageNames: []string{"mon"}},
	{Name: "Nauru", DisplayName: "Nauru", TwoLetterISOLanguageName: "na", ThreeLetterISOLanguageName: "nau", ThreeLetterISOLanguageNames: []string{"nau"}},
	{Name: "Navajo; Navaho", DisplayName: "Navajo; Navaho", TwoLetterISOLanguageName: "nv", ThreeLetterISOLanguageName: "nav", ThreeLetterISOLanguageNames: []string{"nav"}},
	{Name: "Ndebele, North; North Ndebele", DisplayName: "Ndebele, North; North Ndebele", TwoLetterISOLanguageName: "nd", ThreeLetterISOLanguageName: "nde", ThreeLetterISOLanguageNames: []string{"nde"}},
	{Name: "Ndebele, South; South Ndebele", DisplayName: "Ndebele, South; South Ndebele", TwoLetterISOLanguageName: "nr", ThreeLetterISOLanguageName: "nbl", ThreeLetterISOLanguageNames: []string{"nbl"}},
	{Name: "Ndonga", DisplayName: "Ndonga", TwoLetterISOLanguageName: "ng", ThreeLetterISOLanguageName: "ndo", ThreeLetterISOLanguageNames: []string{"ndo"}},
	{Name: "Nepali", DisplayName: "Nepali", TwoLetterISOLanguageName: "ne", ThreeLetterISOLanguageName: "nep", ThreeLetterISOLanguageNames: []string{"nep"}},
	{Name: "Northern Sami", DisplayName: "Northern Sami", TwoLetterISOLanguageName: "se", ThreeLetterISOLanguageName: "sme", ThreeLetterISOLanguageNames: []string{"sme"}},
	{Name: "Norwegian", DisplayName: "Norwegian", TwoLetterISOLanguageName: "no", ThreeLetterISOLanguageName: "nor", ThreeLetterISOLanguageNames: []string{"nor"}},
	{Name: "Norwegian (Bokmal)", DisplayName: "Norwegian (Bokmal)", TwoLetterISOLanguageName: "nb", ThreeLetterISOLanguageName: "nob", ThreeLetterISOLanguageNames: []string{"nob"}},
	{Name: "Norwegian (Nynorsk)", DisplayName: "Norwegian (Nynorsk)", TwoLetterISOLanguageName: "nn", ThreeLetterISOLanguageName: "nno", ThreeLetterISOLanguageNames: []string{"nno"}},
	{Name: "Occitan (post 1500); Provençal", DisplayName: "Occitan (post 1500); Provençal", TwoLetterISOLanguageName: "oc", ThreeLetterISOLanguageName: "oci", ThreeLetterISOLanguageNames: []string{"oci"}},
	{Name: "Ojibwa", DisplayName: "Ojibwa", TwoLetterISOLanguageName: "oj", ThreeLetterISOLanguageName: "oji", ThreeLetterISOLanguageNames: []string{"oji"}},
	{Name: "Oriya", DisplayName: "Oriya", TwoLetterISOLanguageName: "or", ThreeLetterISOLanguageName: "ori", ThreeLetterISOLanguageNames: []string{"ori"}},
	{Name: "Oromo", DisplayName: "Oromo", TwoLetterISOLanguageName: "om", ThreeLetterISOLanguageName: "orm", ThreeLetterISOLanguageNames: []string{"orm"}},
	{Name: "Ossetian; Ossetic", DisplayName: "Ossetian; Ossetic", TwoLetterISOLanguageName: "os", ThreeLetterISOLanguageName: "oss", ThreeLetterISOLanguageNames: []string{"oss"}},
	{Name: "Pali", DisplayName: "Pali", TwoLetterISOLanguageName: "pi", ThreeLetterISOLanguageName: "pli", ThreeLetterISOLanguageNames: []string{"pli"}},
	{Name: "Panjabi; Punjabi", DisplayName: "Panjabi; Punjabi", TwoLetterISOLanguageName: "pa", ThreeLetterISOLanguageName: "pan", ThreeLetterISOLanguageNames: []string{"pan"}},
	{Name: "Persian", DisplayName: "Persian", TwoLetterISOLanguageName: "fa", ThreeLetterISOLanguageName: "fas", ThreeLetterISOLanguageNames: []string{"fas", "per"}},
	{Name: "Polish", DisplayName: "Polish", TwoLetterISOLanguageName: "pl", ThreeLetterISOLanguageName: "pol", ThreeLetterISOLanguageNames: []string{"pol"}},
	{Name: "Portuguese", DisplayName: "Portuguese", TwoLetterISOLanguageName: "pt", ThreeLetterISOLanguageName: "por", ThreeLetterISOLanguageNames: []string{"por"}},
	{Name: "pt-br", DisplayName: "Portuguese (Brazil)", TwoLetterISOLanguageName: "pt-br", ThreeLetterISOLanguageName: "pob", ThreeLetterISOLanguageNames: []string{"pob"}},
	{Name: "pt-pt", DisplayName: "Portuguese (Portugal)", TwoLetterISOLanguageName: "pt-pt", ThreeLetterISOLanguageName: "pop", ThreeLetterISOLanguageNames: []string{"pop"}},
	{Name: "Pushto; Pashto", DisplayName: "Pushto; Pashto", TwoLetterISOLanguageName: "ps", ThreeLetterISOLanguageName: "pus", ThreeLetterISOLanguageNames: []string{"pus"}},
	{Name: "Quechua", DisplayName: "Quechua", TwoLetterISOLanguageName: "qu", ThreeLetterISOLanguageName: "que", ThreeLetterISOLanguageNames: []string{"que"}},
	{Name: "Romanian; Moldavian; Moldovan", DisplayName: "Romanian; Moldavian; Moldovan", TwoLetterISOLanguageName: "ro", ThreeLetterISOLanguageName: "ron", ThreeLetterISOLanguageNames: []string{"ron", "rum"}},
	{Name: "Romansh", DisplayName: "Romansh", TwoLetterISOLanguageName: "rm", ThreeLetterISOLanguageName: "roh", ThreeLetterISOLanguageNames: []string{"roh"}},
	{Name: "Rundi", DisplayName: "Rundi", TwoLetterISOLanguageName: "rn", ThreeLetterISOLanguageName: "run", ThreeLetterISOLanguageNames: []string{"run"}},
	{Name: "Russian", DisplayName: "Russian", TwoLetterISOLanguageName: "ru", ThreeLetterISOLanguageName: "rus", ThreeLetterISOLanguageNames: []string{"rus"}},
	{Name: "Samoan", DisplayName: "Samoan", TwoLetterISOLanguageName: "sm", ThreeLetterISOLanguageName: "smo", ThreeLetterISOLanguageNames: []string{"smo"}},
	{Name: "Sango", DisplayName: "Sango", TwoLetterISOLanguageName: "sg", ThreeLetterISOLanguageName: "sag", ThreeLetterISOLanguageNames: []string{"sag"}},
	{Name: "Sanskrit", DisplayName: "Sanskrit", TwoLetterISOLanguageName: "sa", ThreeLetterISOLanguageName: "san", ThreeLetterISOLanguageNames: []string{"san"}},
	{Name: "Sardinian", DisplayName: "Sardinian", TwoLetterISOLanguageName: "sc", ThreeLetterISOLanguageName: "srd", ThreeLetterISOLanguageNames: []string{"srd"}},
	{Name: "Serbian", DisplayName: "Serbian", TwoLetterISOLanguageName: "sr", ThreeLetterISOLanguageName: "srp", ThreeLetterISOLanguageNames: []string{"srp"}},
	{Name: "Shona", DisplayName: "Shona", TwoLetterISOLanguageName: "sn", ThreeLetterISOLanguageName: "sna", ThreeLetterISOLanguageNames: []string{"sna"}},
	{Name: "Sichuan Yi; Nuosu", DisplayName: "Sichuan Yi; Nuosu", TwoLetterISOLanguageName: "ii", ThreeLetterISOLanguageName: "iii", ThreeLetterISOLanguageNames: []string{"iii"}},
	{Name: "Sindhi", DisplayName: "Sindhi", TwoLetterISOLanguageName: "sd", ThreeLetterISOLanguageName: "snd", ThreeLetterISOLanguageNames: []string{"snd"}},
	{Name: "Sinhala; Sinhalese", DisplayName: "Sinhala; Sinhalese", TwoLetterISOLanguageName: "si", ThreeLetterISOLanguageName: "sin", ThreeLetterISOLanguageNames: []string{"sin"}},
	{Name: "Slovak", DisplayName: "Slovak", TwoLetterISOLanguageName: "sk", ThreeLetterISOLanguageName: "slk", ThreeLetterISOLanguageNames: []string{"slk", "slo"}},
	{Name: "Slovenian", DisplayName: "Slovenian", TwoLetterISOLanguageName: "sl", ThreeLetterISOLanguageName: "slv", ThreeLetterISOLanguageNames: []string{"slv"}},
	{Name: "Somali", DisplayName: "Somali", TwoLetterISOLanguageName: "so", ThreeLetterISOLanguageName: "som", ThreeLetterISOLanguageNames: []string{"som"}},
	{Name: "Sotho, Southern", DisplayName: "Sotho, Southern", TwoLetterISOLanguageName: "st", ThreeLetterISOLanguageName: "sot", ThreeLetterISOLanguageNames: []string{"sot"}},
	{Name: "Spanish; Castilian", DisplayName: "Spanish; Castilian", TwoLetterISOLanguageName: "es", ThreeLetterISOLanguageName: "spa", ThreeLetterISOLanguageNames: []string{"spa"}},
	{Name: "es-419", DisplayName: "Spanish; Latin", TwoLetterISOLanguageName: "es-419", ThreeLetterISOLanguageName: "spa", ThreeLetterISOLanguageNames: []string{"spa"}},
	{Name: "Sundanese", DisplayName: "Sundanese", TwoLetterISOLanguageName: "su", ThreeLetterISOLanguageName: "sun", ThreeLetterISOLanguageNames: []string{"sun"}},
	{Name: "Swahili", DisplayName: "Swahili", TwoLetterISOLanguageName: "sw", ThreeLetterISOLanguageName: "swa", ThreeLetterISOLanguageNames: []string{"swa"}},
	{Name: "Swati", DisplayName: "Swati", TwoLetterISOLanguageName: "ss", ThreeLetterISOLanguageName: "ssw", ThreeLetterISOLanguageNames: []string{"ssw"}},
	{Name: "Swedish", DisplayName: "Swedish", TwoLetterISOLanguageName: "sv", ThreeLetterISOLanguageName: "swe", ThreeLetterISOLanguageNames: []string{"swe"}},
	{Name: "Tagalog", DisplayName: "Tagalog", TwoLetterISOLanguageName: "tl", ThreeLetterISOLanguageName: "tgl", ThreeLetterISOLanguageNames: []string{"tgl"}},
	{Name: "Tahitian", DisplayName: "Tahitian", TwoLetterISOLanguageName: "ty", ThreeLetterISOLanguageName: "tah", ThreeLetterISOLanguageNames: []string{"tah"}},
	{Name: "Tajik", DisplayName: "Tajik", TwoLetterISOLanguageName: "tg", ThreeLetterISOLanguageName: "tgk", ThreeLetterISOLanguageNames: []string{"tgk"}},
	{Name: "Tamil", DisplayName: "Tamil", TwoLetterISOLanguageName: "ta", ThreeLetterISOLanguageName: "tam", ThreeLetterISOLanguageNames: []string{"tam"}},
	{Name: "Tatar", DisplayName: "Tatar", TwoLetterISOLanguageName: "tt", ThreeLetterISOLanguageName: "tat", ThreeLetterISOLanguageNames: []string{"tat"}},
	{Name: "Telugu", DisplayName: "Telugu", TwoLetterISOLanguageName: "te", ThreeLetterISOLanguageName: "tel", ThreeLetterISOLanguageNames: []string{"tel"}},
	{Name: "Thai", DisplayName: "Thai", TwoLetterISOLanguageName: "th", ThreeLetterISOLanguageName: "tha", ThreeLetterISOLanguageNames: []string{"tha"}},
	{Name: "Tibetan", DisplayName: "Tibetan", TwoLetterISOLanguageName: "bo", ThreeLetterISOLanguageName: "bod", ThreeLetterISOLanguageNames: []string{"bod", "tib"}},
	{Name: "Tigrinya", DisplayName: "Tigrinya", TwoLetterISOLanguageName: "ti", ThreeLetterISOLanguageName: "tir", ThreeLetterISOLanguageNames: []string{"tir"}},
	{Name: "Tonga (Tonga Islands)", DisplayName: "Tonga (Tonga Islands)", TwoLetterISOLanguageName: "to", ThreeLetterISOLanguageName: "ton", ThreeLetterISOLanguageNames: []string{"ton"}},
	{Name: "Tsonga", DisplayName: "Tsonga", TwoLetterISOLanguageName: "ts", ThreeLetterISOLanguageName: "tso", ThreeLetterISOLanguageNames: []string{"tso"}},
	{Name: "Tswana", DisplayName: "Tswana", TwoLetterISOLanguageName: "tn", ThreeLetterISOLanguageName: "tsn", ThreeLetterISOLanguageNames: []string{"tsn"}},
	{Name: "Turkish", DisplayName: "Turkish", TwoLetterISOLanguageName: "tr", ThreeLetterISOLanguageName: "tur", ThreeLetterISOLanguageNames: []string{"tur"}},
	{Name: "Turkmen", DisplayName: "Turkmen", TwoLetterISOLanguageName: "tk", ThreeLetterISOLanguageName: "tuk", ThreeLetterISOLanguageNames: []string{"tuk"}},
	{Name: "Twi", DisplayName: "Twi", TwoLetterISOLanguageName: "tw", ThreeLetterISOLanguageName: "twi", ThreeLetterISOLanguageNames: []string{"twi"}},
	{Name: "Uighur; Uyghur", DisplayName: "Uighur; Uyghur", TwoLetterISOLanguageName: "ug", ThreeLetterISOLanguageName: "uig", ThreeLetterISOLanguageNames: []string{"uig"}},
	{Name: "Ukrainian", DisplayName: "Ukrainian", TwoLetterISOLanguageName: "uk", ThreeLetterISOLanguageName: "ukr", ThreeLetterISOLanguageNames: []string{"ukr"}},
	{Name: "Urdu", DisplayName: "Urdu", TwoLetterISOLanguageName: "ur", ThreeLetterISOLanguageName: "urd", ThreeLetterISOLanguageNames: []string{"urd"}},
	{Name: "Uzbek", DisplayName: "Uzbek", TwoLetterISOLanguageName: "uz", ThreeLetterISOLanguageName: "uzb", ThreeLetterISOLanguageNames: []string{"uzb"}},
	{Name: "Venda", DisplayName: "Venda", TwoLetterISOLanguageName: "ve", ThreeLetterISOLanguageName: "ven", ThreeLetterISOLanguageNames: []string{"ven"}},
	{Name: "Vietnamese", DisplayName: "Vietnamese", TwoLetterISOLanguageName: "vi", ThreeLetterISOLanguageName: "vie", ThreeLetterISOLanguageNames: []string{"vie"}},
	{Name: "Volapük", DisplayName: "Volapük", TwoLetterISOLanguageName: "vo", ThreeLetterISOLanguageName: "vol", ThreeLetterISOLanguageNames: []string{"vol"}},
	{Name: "Walloon", DisplayName: "Walloon", TwoLetterISOLanguageName: "wa", ThreeLetterISOLanguageName: "wln", ThreeLetterISOLanguageNames: []string{"wln"}},
	{Name: "Welsh", DisplayName: "Welsh", TwoLetterISOLanguageName: "cy", ThreeLetterISOLanguageName: "cym", ThreeLetterISOLanguageNames: []string{"cym", "wel"}},
	{Name: "Western Frisian", DisplayName: "Western Frisian", TwoLetterISOLanguageName: "fy", ThreeLetterISOLanguageName: "fry", ThreeLetterISOLanguageNames: []string{"fry"}},
	{Name: "Wolof", DisplayName: "Wolof", TwoLetterISOLanguageName: "wo", ThreeLetterISOLanguageName: "wol", ThreeLetterISOLanguageNames: []string{"wol"}},
	{Name: "Xhosa", DisplayName: "Xhosa", TwoLetterISOLanguageName: "xh", ThreeLetterISOLanguageName: "xho", ThreeLetterISOLanguageNames: []string{"xho"}},
	{Name: "Yiddish", DisplayName: "Yiddish", TwoLetterISOLanguageName: "yi", ThreeLetterISOLanguageName: "yid", ThreeLetterISOLanguageNames: []string{"yid"}},
	{Name: "Yoruba", DisplayName: "Yoruba", TwoLetterISOLanguageName: "yo", ThreeLetterISOLanguageName: "yor", ThreeLetterISOLanguageNames: []string{"yor"}},
	{Name: "Zhuang; Chuang", DisplayName: "Zhuang; Chuang", TwoLetterISOLanguageName: "za", ThreeLetterISOLanguageName: "zha", ThreeLetterISOLanguageNames: []string{"zha"}},
	{Name: "Zulu", DisplayName: "Zulu", TwoLetterISOLanguageName: "zu", ThreeLetterISOLanguageName: "zul", ThreeLetterISOLanguageNames: []string{"zul"}}}

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
