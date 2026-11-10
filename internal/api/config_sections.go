package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Named configuration sections (GET|POST /System/Configuration/{key}): the
// encoding (Transcoding page), branding, metadata (Libraries - Display) and
// xbmc metadata (Libraries - NFO) sections are stored as one JSON document
// each. A POST replaces the whole section: members left out fall back to
// their defaults, unknown members are ignored (admin.config.R3). Tinyfin keeps
// the values and serves them back; the encoding ones are not all honored by
// the transcoder (admin.config.R5), and the metadata and xbmc metadata members
// describe behavior this server does not have at all (admin.config.R9/.R10).

const (
	settingEncodingConfig = "EncodingConfiguration"
	settingBrandingConfig = "BrandingConfiguration"
	settingMetadataConfig = "MetadataConfiguration"
	settingXbmcMetadata   = "XbmcMetadataOptions"
)

// encodingConfigurationDto is the encoding section with the reference's
// defaults.
type encodingConfigurationDto struct {
	EncodingThreadCount                                       int      `json:"EncodingThreadCount"`
	EnableFallbackFont                                        bool     `json:"EnableFallbackFont"`
	EnableAudioVbr                                            bool     `json:"EnableAudioVbr"`
	DownMixAudioBoost                                         float64  `json:"DownMixAudioBoost"`
	DownMixStereoAlgorithm                                    string   `json:"DownMixStereoAlgorithm"`
	MaxMuxingQueueSize                                        int      `json:"MaxMuxingQueueSize"`
	EnableThrottling                                          bool     `json:"EnableThrottling"`
	ThrottleDelaySeconds                                      int      `json:"ThrottleDelaySeconds"`
	EnableSegmentDeletion                                     bool     `json:"EnableSegmentDeletion"`
	SegmentKeepSeconds                                        int      `json:"SegmentKeepSeconds"`
	HardwareAccelerationType                                  string   `json:"HardwareAccelerationType"`
	EncoderAppPathDisplay                                     string   `json:"EncoderAppPathDisplay,omitempty"`
	VaapiDevice                                               string   `json:"VaapiDevice"`
	QsvDevice                                                 string   `json:"QsvDevice"`
	EnableTonemapping                                         bool     `json:"EnableTonemapping"`
	EnableVppTonemapping                                      bool     `json:"EnableVppTonemapping"`
	EnableVideoToolboxTonemapping                             bool     `json:"EnableVideoToolboxTonemapping"`
	TonemappingAlgorithm                                      string   `json:"TonemappingAlgorithm"`
	TonemappingMode                                           string   `json:"TonemappingMode"`
	TonemappingRange                                          string   `json:"TonemappingRange"`
	TonemappingDesat                                          float64  `json:"TonemappingDesat"`
	TonemappingPeak                                           float64  `json:"TonemappingPeak"`
	TonemappingParam                                          float64  `json:"TonemappingParam"`
	VppTonemappingBrightness                                  float64  `json:"VppTonemappingBrightness"`
	VppTonemappingContrast                                    float64  `json:"VppTonemappingContrast"`
	H264Crf                                                   int      `json:"H264Crf"`
	H265Crf                                                   int      `json:"H265Crf"`
	EncoderPreset                                             string   `json:"EncoderPreset"`
	DeinterlaceDoubleRate                                     bool     `json:"DeinterlaceDoubleRate"`
	DeinterlaceMethod                                         string   `json:"DeinterlaceMethod"`
	EnableDecodingColorDepth10Hevc                            bool     `json:"EnableDecodingColorDepth10Hevc"`
	EnableDecodingColorDepth10Vp9                             bool     `json:"EnableDecodingColorDepth10Vp9"`
	EnableDecodingColorDepth10HevcRext                        bool     `json:"EnableDecodingColorDepth10HevcRext"`
	EnableDecodingColorDepth12HevcRext                        bool     `json:"EnableDecodingColorDepth12HevcRext"`
	EnableEnhancedNvdecDecoder                                bool     `json:"EnableEnhancedNvdecDecoder"`
	PreferSystemNativeHwDecoder                               bool     `json:"PreferSystemNativeHwDecoder"`
	EnableIntelLowPowerH264HwEncoder                          bool     `json:"EnableIntelLowPowerH264HwEncoder"`
	EnableIntelLowPowerHevcHwEncoder                          bool     `json:"EnableIntelLowPowerHevcHwEncoder"`
	EnableHardwareEncoding                                    bool     `json:"EnableHardwareEncoding"`
	AllowHevcEncoding                                         bool     `json:"AllowHevcEncoding"`
	AllowAv1Encoding                                          bool     `json:"AllowAv1Encoding"`
	EnableSubtitleExtraction                                  bool     `json:"EnableSubtitleExtraction"`
	SubtitleExtractionTimeoutMinutes                          int      `json:"SubtitleExtractionTimeoutMinutes"`
	HardwareDecodingCodecs                                    []string `json:"HardwareDecodingCodecs"`
	AllowOnDemandMetadataBasedKeyframeExtractionForExtensions []string `json:"AllowOnDemandMetadataBasedKeyframeExtractionForExtensions"`
	HlsAudioSeekStrategy                                      string   `json:"HlsAudioSeekStrategy"`
}

func defaultEncodingConfiguration() encodingConfigurationDto {
	return encodingConfigurationDto{
		EncodingThreadCount: -1, DownMixAudioBoost: 2, DownMixStereoAlgorithm: "None",
		MaxMuxingQueueSize: 2048, ThrottleDelaySeconds: 180, SegmentKeepSeconds: 720,
		HardwareAccelerationType: "none", VaapiDevice: "/dev/dri/renderD128",
		TonemappingAlgorithm: "bt2390", TonemappingMode: "auto", TonemappingRange: "auto",
		TonemappingPeak: 100, VppTonemappingBrightness: 16, VppTonemappingContrast: 1,
		H264Crf: 23, H265Crf: 28, EncoderPreset: "auto", DeinterlaceMethod: "yadif",
		EnableDecodingColorDepth10Hevc: true, EnableDecodingColorDepth10Vp9: true,
		EnableEnhancedNvdecDecoder: true, PreferSystemNativeHwDecoder: true,
		EnableHardwareEncoding: true, EnableSubtitleExtraction: true, SubtitleExtractionTimeoutMinutes: 30,
		HardwareDecodingCodecs: []string{"h264", "vc1"},
		AllowOnDemandMetadataBasedKeyframeExtractionForExtensions: []string{"mkv"},
		HlsAudioSeekStrategy: "TrimCopiedAudio",
	}
}

// loadSection decodes the stored JSON document of a section over def.
func (s *Server) loadSection(ctx context.Context, key string, v any) {
	if raw, ok, err := s.store.GetSetting(ctx, key); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), v)
	}
}

func (s *Server) handleEncodingConfigurationGet(w http.ResponseWriter, r *http.Request) {
	cfg := defaultEncodingConfiguration()
	s.loadSection(r.Context(), settingEncodingConfig, &cfg)
	cfg.EncoderAppPathDisplay = s.cfg.TranscodeFFmpegPath
	writeJSON(w, http.StatusOK, cfg)
}

// hardwareAccelerationTypes are the accepted HardwareAccelerationType values.
var hardwareAccelerationTypes = []string{"none", "amf", "qsv", "nvenc", "vaapi", "rkmpp", "videotoolbox", "v4l2m2m"}

// canonicalHardwareAccelerationType matches a name ignoring case and
// surrounding whitespace, as the reference does. Numeric spellings are not
// names and are refused.
func canonicalHardwareAccelerationType(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, t := range hardwareAccelerationTypes {
		if v == t {
			return t, true
		}
	}
	return "", false
}

func (s *Server) handleEncodingConfigurationPost(w http.ResponseWriter, r *http.Request) {
	cfg := defaultEncodingConfiguration()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || json.Unmarshal(body, &cfg) != nil {
		writeInvalidBody(w)
		return
	}
	// admin.config.R6: an unknown HardwareAccelerationType is refused (400);
	// the reference answers 500 here. A known one is stored in its canonical
	// lower-case spelling.
	var members map[string]json.RawMessage
	_ = json.Unmarshal(body, &members)
	if _, present := members["HardwareAccelerationType"]; present {
		canon, ok := canonicalHardwareAccelerationType(cfg.HardwareAccelerationType)
		if !ok || string(members["HardwareAccelerationType"]) == "null" {
			writeProblemDoc(w, http.StatusBadRequest, problemTypeBadRequest, "One or more validation errors occurred.", nil)
			return
		}
		cfg.HardwareAccelerationType = canon
	}
	cfg.EncoderAppPathDisplay = ""
	if cfg.HardwareDecodingCodecs == nil {
		cfg.HardwareDecodingCodecs = []string{}
	}
	if cfg.AllowOnDemandMetadataBasedKeyframeExtractionForExtensions == nil {
		cfg.AllowOnDemandMetadataBasedKeyframeExtractionForExtensions = []string{}
	}
	raw, _ := json.Marshal(cfg)
	if err := s.store.SetSetting(r.Context(), settingEncodingConfig, string(raw)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// brandingConfigurationDto is the branding section. LoginDisclaimer and
// CustomCss are null (left out of the JSON) until set.
type brandingConfigurationDto struct {
	LoginDisclaimer     *string `json:"LoginDisclaimer,omitempty"`
	CustomCss           *string `json:"CustomCss,omitempty"`
	SplashscreenEnabled bool    `json:"SplashscreenEnabled"`
}

func (s *Server) brandingConfiguration(ctx context.Context) brandingConfigurationDto {
	var cfg brandingConfigurationDto
	s.loadSection(ctx, settingBrandingConfig, &cfg)
	return cfg
}

// handleBrandingConfigurationGet is GET /System/Configuration/branding (any
// signed-in user, like the other sections).
func (s *Server) handleBrandingConfigurationGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.brandingConfiguration(r.Context()))
}

func (s *Server) handleBrandingConfigurationPost(w http.ResponseWriter, r *http.Request) {
	var cfg brandingConfigurationDto
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeInvalidBody(w)
		return
	}
	raw, _ := json.Marshal(cfg)
	if err := s.store.SetSetting(r.Context(), settingBrandingConfig, string(raw)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBrandingCss is GET /Branding/Css and /Branding/Css.css (anonymous):
// the stored custom stylesheet, empty when none is set.
func (s *Server) handleBrandingCss(w http.ResponseWriter, r *http.Request) {
	css := ""
	if c := s.brandingConfiguration(r.Context()).CustomCss; c != nil {
		css = *c
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(css))
}

// metadataConfigurationDto is the metadata section (Libraries - Display).
// The one member picks which timestamp becomes an item's DateAdded: the file's
// own one (true) or the moment the library scanned it in (false). This server
// has only the first source (internal/scan's fileDateAdded), so true is both
// the default and the only setting it acts on — the reference's default as
// well; choosing false is stored and served back and changes nothing
// (admin.config.R9, DIV-30 in docs/13-divergences.md).
type metadataConfigurationDto struct {
	UseFileCreationTimeForDateAdded bool `json:"UseFileCreationTimeForDateAdded"`
}

func defaultMetadataConfiguration() metadataConfigurationDto {
	return metadataConfigurationDto{UseFileCreationTimeForDateAdded: true}
}

func (s *Server) metadataConfiguration(ctx context.Context) metadataConfigurationDto {
	cfg := defaultMetadataConfiguration()
	s.loadSection(ctx, settingMetadataConfig, &cfg)
	return cfg
}

// handleMetadataConfigurationGet is GET /System/Configuration/metadata (any
// signed-in user, like the other sections).
func (s *Server) handleMetadataConfigurationGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metadataConfiguration(r.Context()))
}

func (s *Server) handleMetadataConfigurationPost(w http.ResponseWriter, r *http.Request) {
	cfg := defaultMetadataConfiguration()
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeInvalidBody(w)
		return
	}
	raw, _ := json.Marshal(cfg)
	if err := s.store.SetSetting(r.Context(), settingMetadataConfig, string(raw)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// xbmcMetadataOptionsDto is the xbmc metadata section (Libraries - NFO): what
// a Kodi-style sidecar would carry. This server writes no sidecar file at all,
// so every member is stored and served back and none is honored
// (admin.config.R10, DIV-31 in docs/13-divergences.md). UserId is absent until
// a value is sent; an empty string is a value and is served back as one.
type xbmcMetadataOptionsDto struct {
	UserID                       *string `json:"UserId,omitempty"`
	ReleaseDateFormat            string  `json:"ReleaseDateFormat"`
	SaveImagePathsInNfo          bool    `json:"SaveImagePathsInNfo"`
	EnablePathSubstitution       bool    `json:"EnablePathSubstitution"`
	EnableExtraThumbsDuplication bool    `json:"EnableExtraThumbsDuplication"`
}

func defaultXbmcMetadataOptions() xbmcMetadataOptionsDto {
	return xbmcMetadataOptionsDto{
		ReleaseDateFormat:            "yyyy-MM-dd",
		SaveImagePathsInNfo:          true,
		EnablePathSubstitution:       true,
		EnableExtraThumbsDuplication: false,
	}
}

func (s *Server) xbmcMetadataOptions(ctx context.Context) xbmcMetadataOptionsDto {
	cfg := defaultXbmcMetadataOptions()
	s.loadSection(ctx, settingXbmcMetadata, &cfg)
	return cfg
}

// handleXbmcMetadataOptionsGet is GET /System/Configuration/xbmcmetadata (any
// signed-in user).
func (s *Server) handleXbmcMetadataOptionsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.xbmcMetadataOptions(r.Context()))
}

func (s *Server) handleXbmcMetadataOptionsPost(w http.ResponseWriter, r *http.Request) {
	cfg := defaultXbmcMetadataOptions()
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeInvalidBody(w)
		return
	}
	raw, _ := json.Marshal(cfg)
	if err := s.store.SetSetting(r.Context(), settingXbmcMetadata, string(raw)); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
