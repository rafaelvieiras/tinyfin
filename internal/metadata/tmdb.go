package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// tmdbImageBase is where TMDB's poster_path/backdrop_path/still_path
// values (root-relative, e.g. "/abc123.jpg") resolve against — TMDB's
// publicly documented image CDN, not configurable per-request.
const tmdbImageBase = "https://image.tmdb.org/t/p/original"

// TMDBProvider is the direct-provider reference implementation D3 calls
// for (docs/00-decision-log.md, docs/08) — queries The Movie Database's
// public v3 REST API directly over HTTP. This is ordinary third-party API
// integration against TMDB's own published contract; it has no bearing on
// (and no proximity to) this project's Jellyfin clean-room requirement,
// which is about the *reference media server's* source, not an unrelated
// metadata API Jellyfin itself also happens to integrate with.
type TMDBProvider struct {
	apiKey     string
	baseURL    string // overridable in tests; defaults to TMDB's real API root
	httpClient *http.Client
}

// NewTMDBProvider builds a TMDBProvider. apiKey is required — construct a
// NoopProvider instead if none is configured (see that type's doc
// comment); this constructor does not silently degrade into one, so a
// caller can't accidentally end up with a "provider" that can never
// succeed without realizing why.
func NewTMDBProvider(apiKey string) *TMDBProvider {
	return &TMDBProvider{
		apiKey:     apiKey,
		baseURL:    "https://api.themoviedb.org/3",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *TMDBProvider) Lookup(ctx context.Context, q Query) (Result, error) {
	switch q.Kind {
	case KindMovie:
		return p.lookupMovie(ctx, q)
	case KindSeries:
		return p.lookupSeries(ctx, q)
	case KindEpisode:
		return p.lookupEpisode(ctx, q)
	default:
		return Result{}, &ProviderError{Code: "bad_query", Message: "unknown Kind: " + string(q.Kind)}
	}
}

type tmdbSearchResult struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Name          string  `json:"name"` // TV search uses Name, not Title
	OriginalName  string  `json:"original_name"`
	Overview      string  `json:"overview"`
	ReleaseDate   string  `json:"release_date"`   // movies
	FirstAirDate  string  `json:"first_air_date"` // TV
	VoteAverage   float64 `json:"vote_average"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
}

type tmdbSearchResponse struct {
	Results []tmdbSearchResult `json:"results"`
}

type tmdbGenre struct {
	Name string `json:"name"`
}

type tmdbDetailsResponse struct {
	tmdbSearchResult
	Genres  []tmdbGenre `json:"genres"`
	Tagline string      `json:"tagline"`
	// Credits is populated by requesting append_to_response=credits on
	// the details call (lookupMovie) — TMDB's documented way to fold a
	// second endpoint's response into the first request instead of
	// issuing /movie/{id}/credits separately.
	Credits tmdbCredits `json:"credits"`
}

// tmdbCredits mirrors the shape TMDB's /movie/{id}/credits endpoint (or
// append_to_response=credits on /movie/{id}) returns — trimmed to the
// fields creditsToPeople below actually uses.
type tmdbCredits struct {
	Cast []tmdbCastMember `json:"cast"`
	Crew []tmdbCrewMember `json:"crew"`
}

type tmdbCastMember struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	Order       int    `json:"order"`
	ProfilePath string `json:"profile_path"`
}

type tmdbCrewMember struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Job         string `json:"job"`
	ProfilePath string `json:"profile_path"`
}

// maxCastCredits bounds how many cast entries lookupMovie carries onto a
// Result.People — a movie's full TMDB cast list can run to 50+ people, most
// of them uncredited bit parts a detail page never needs; this project's
// own instruction for this feature calls out this exact bound to avoid an
// oversized item response.
const maxCastCredits = 20

// crewTypeForJob maps a TMDB crew "job" string to the Person.Type enum a
// BaseItemPerson's Type field uses on the wire (docs/12-adjacent: same
// enum the reference server's own OpenAPI schema documents for
// BaseItemPerson, confirmed independently of this project's TMDB
// integration). Only the jobs this project's own task instruction calls
// out map to something — every other TMDB crew department (camera, sound,
// costume, ...) is deliberately left unmapped rather than guessing at a
// Type value the reference server wouldn't recognize.
func crewTypeForJob(job string) (string, bool) {
	switch job {
	case "Director":
		return "Director", true
	case "Writer", "Screenplay", "Story":
		return "Writer", true
	case "Producer":
		return "Producer", true
	default:
		return "", false
	}
}

// creditsToPeople converts TMDB's cast/crew shape into this package's own
// Result.People — cast ordered by TMDB's own "order" field (billing
// order), capped at maxCastCredits; crew filtered down to only the jobs
// crewTypeForJob recognizes.
func creditsToPeople(c tmdbCredits) []Person {
	cast := append([]tmdbCastMember(nil), c.Cast...)
	sort.Slice(cast, func(i, j int) bool { return cast[i].Order < cast[j].Order })
	if len(cast) > maxCastCredits {
		cast = cast[:maxCastCredits]
	}

	people := make([]Person, 0, len(cast)+len(c.Crew))
	for _, m := range cast {
		people = append(people, Person{Name: m.Name, Role: m.Character, Type: "Actor", TMDBID: m.ID})
	}
	for _, m := range c.Crew {
		t, ok := crewTypeForJob(m.Job)
		if !ok {
			continue
		}
		people = append(people, Person{Name: m.Name, Type: t, TMDBID: m.ID})
	}
	return people
}

type tmdbEpisodeResponse struct {
	Name        string  `json:"name"`
	Overview    string  `json:"overview"`
	AirDate     string  `json:"air_date"`
	VoteAverage float64 `json:"vote_average"`
	StillPath   string  `json:"still_path"`
}

func (p *TMDBProvider) lookupMovie(ctx context.Context, q Query) (Result, error) {
	search, err := p.get(ctx, "/search/movie", url.Values{
		"query": {q.Title},
		"year":  {yearParam(q.Year)},
	})
	if err != nil {
		return Result{}, err
	}
	var sr tmdbSearchResponse
	if err := json.Unmarshal(search, &sr); err != nil {
		return Result{}, &ProviderError{Code: "bad_response", Message: err.Error()}
	}
	if len(sr.Results) == 0 {
		return Result{}, ErrNotFound
	}

	// append_to_response=credits folds /movie/{id}/credits into this same
	// call — simpler to integrate than a second request, and TMDB's own
	// documented way to do exactly this (this method's one call stays a
	// single HTTP round trip either way).
	details, err := p.get(ctx, fmt.Sprintf("/movie/%d", sr.Results[0].ID), url.Values{"append_to_response": {"credits"}})
	if err != nil {
		return Result{}, err
	}
	var d tmdbDetailsResponse
	if err := json.Unmarshal(details, &d); err != nil {
		return Result{}, &ProviderError{Code: "bad_response", Message: err.Error()}
	}

	res := detailsToResult(d)
	res.Title = d.Title
	res.OriginalTitle = d.OriginalTitle
	if d.Tagline != "" {
		res.Tags = []string{d.Tagline}
	}
	res.PremiereDateUnix = parseTMDBDate(d.ReleaseDate)
	res.ExternalIDs = map[string]string{"Tmdb": strconv.Itoa(d.ID)}
	res.People = creditsToPeople(d.Credits)
	return res, nil
}

func (p *TMDBProvider) lookupSeries(ctx context.Context, q Query) (Result, error) {
	id, err := p.resolveSeriesID(ctx, q)
	if err != nil {
		return Result{}, err
	}

	details, err := p.get(ctx, fmt.Sprintf("/tv/%d", id), nil)
	if err != nil {
		return Result{}, err
	}
	var d tmdbDetailsResponse
	if err := json.Unmarshal(details, &d); err != nil {
		return Result{}, &ProviderError{Code: "bad_response", Message: err.Error()}
	}

	res := detailsToResult(d)
	res.Title = d.Name
	res.OriginalTitle = d.OriginalName
	res.PremiereDateUnix = parseTMDBDate(d.FirstAirDate)
	res.ExternalIDs = map[string]string{"Tmdb": strconv.Itoa(id)}
	return res, nil
}

func (p *TMDBProvider) lookupEpisode(ctx context.Context, q Query) (Result, error) {
	seriesID, err := p.resolveSeriesID(ctx, q)
	if err != nil {
		return Result{}, err
	}

	body, err := p.get(ctx, fmt.Sprintf("/tv/%d/season/%d/episode/%d", seriesID, q.SeasonNumber, q.EpisodeNumber), nil)
	if err != nil {
		return Result{}, err
	}
	var e tmdbEpisodeResponse
	if err := json.Unmarshal(body, &e); err != nil {
		return Result{}, &ProviderError{Code: "bad_response", Message: err.Error()}
	}
	if e.Name == "" && e.Overview == "" {
		return Result{}, ErrNotFound
	}

	var images []Image
	if e.StillPath != "" {
		images = append(images, Image{Kind: "Primary", URL: tmdbImageBase + e.StillPath})
	}
	return Result{
		Title:            e.Name,
		Overview:         e.Overview,
		CommunityRating:  e.VoteAverage,
		PremiereDateUnix: parseTMDBDate(e.AirDate),
		Images:           images,
		ExternalIDs:      map[string]string{"Tmdb": strconv.Itoa(seriesID)},
	}, nil
}

// resolveSeriesID returns the TMDB series ID for q's parent series: reused
// from q.ExternalIDs["Tmdb"] if the caller already resolved it (a prior
// Series-kind Lookup in the same scan — see internal/scan), otherwise
// searched fresh by SeriesTitle so an episode Lookup is correct standalone
// too.
func (p *TMDBProvider) resolveSeriesID(ctx context.Context, q Query) (int, error) {
	if v, ok := q.ExternalIDs["Tmdb"]; ok && v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			return id, nil
		}
	}

	search, err := p.get(ctx, "/search/tv", url.Values{"query": {q.SeriesTitle}})
	if err != nil {
		return 0, err
	}
	var sr tmdbSearchResponse
	if err := json.Unmarshal(search, &sr); err != nil {
		return 0, &ProviderError{Code: "bad_response", Message: err.Error()}
	}
	if len(sr.Results) == 0 {
		return 0, ErrNotFound
	}
	return sr.Results[0].ID, nil
}

func detailsToResult(d tmdbDetailsResponse) Result {
	genres := make([]string, 0, len(d.Genres))
	for _, g := range d.Genres {
		genres = append(genres, g.Name)
	}
	var images []Image
	if d.PosterPath != "" {
		images = append(images, Image{Kind: "Primary", URL: tmdbImageBase + d.PosterPath})
	}
	if d.BackdropPath != "" {
		images = append(images, Image{Kind: "Backdrop", URL: tmdbImageBase + d.BackdropPath})
	}
	return Result{
		Overview:        d.Overview,
		Genres:          genres,
		CommunityRating: d.VoteAverage,
		Images:          images,
	}
}

func yearParam(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

// parseTMDBDate converts TMDB's "YYYY-MM-DD" date strings to Unix seconds
// (Result.PremiereDateUnix's doc comment) — 0 on empty/unparseable input,
// matching Query/Result's "zero value means unknown" convention throughout
// this package.
func parseTMDBDate(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// get performs an authenticated GET against the TMDB API and returns the
// raw response body, translating non-2xx responses into a *ProviderError
// rather than letting a caller's json.Unmarshal fail on an HTML/plain-text
// error page.
func (p *TMDBProvider) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("api_key", p.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, &ProviderError{Code: "transport", Message: err.Error()}
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "transport", Message: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &ProviderError{Code: "transport", Message: err.Error()}
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &ProviderError{Code: "rate_limited", Message: "TMDB API rate limit exceeded"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ProviderError{Code: "tmdb_error", Message: fmt.Sprintf("TMDB API returned status %d", resp.StatusCode)}
	}
	return body, nil
}
