package transcode

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rafaelvieiras/tinyfin/internal/subtitle"
)

// bitmapCodecs are the ffprobe codec names of picture subtitles.
var bitmapCodecs = map[string]bool{
	"dvd_subtitle":      true,
	"hdmv_pgs_subtitle": true,
	"dvb_subtitle":      true,
	"xsub":              true,
}

// sidecarContainers are the extensions of external subtitle files that may hold
// picture subtitles: whether they do is decided by what ffprobe finds inside.
var sidecarContainers = map[string]bool{".idx": true, ".sub": true, ".sup": true, ".mks": true}

type probedStream struct {
	Index         int    `json:"index"`
	CodecType     string `json:"codec_type"`
	CodecName     string `json:"codec_name"`
	Width, Height int
}

// probeStreams lists the streams of path with their size.
func (c Config) probeStreams(path string) []probedStream {
	out, err := exec.Command(c.ffprobePath(), "-v", "error", "-show_entries",
		"stream=index,codec_type,codec_name,width,height", "-of", "json", path).Output()
	if err != nil {
		return nil
	}
	var doc struct {
		Streams []probedStream `json:"streams"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return nil
	}
	return doc.Streams
}

// resolveBurn completes the burn-in description with what only the files can
// tell: whether an external sidecar holds pictures, the size of the subtitle
// stream (the canvas its pictures were authored for) and the size of the video.
// Failing to probe leaves the fields zero, and the graph then drops what it
// needs them for.
func (c Config) resolveBurn(req *Request, jobDir string) {
	sub := req.Subtitle
	if sub == nil || req.VideoCodec == "" || req.VideoCodec == "copy" || req.VideoCodec == "none" {
		return
	}
	if !sub.Embedded && (!sub.Bitmap || sub.External) && sub.Path != "" &&
		sidecarContainers[strings.ToLower(filepath.Ext(sub.Path))] {
		for _, s := range c.probeStreams(sub.Path) {
			if s.CodecType != "subtitle" {
				continue
			}
			if bitmapCodecs[s.CodecName] {
				sub.Bitmap, sub.External, sub.StreamIndex = true, true, s.Index
				sub.Width, sub.Height = s.Width, s.Height
			}
			break
		}
	}
	if !sub.Bitmap {
		c.resolveText(req, jobDir)
		return
	}
	for _, s := range c.probeStreams(req.InputPath) {
		if s.CodecType == "video" && req.SourceWidth == 0 {
			req.SourceWidth, req.SourceHeight = s.Width, s.Height
		}
		if !sub.External && s.Index == sub.StreamIndex {
			sub.Width, sub.Height = s.Width, s.Height
		}
	}
}

// outputSize is the size of the picture after the scaling of the request.
func (req Request) outputSize() (w, h int) {
	w, h = req.SourceWidth, req.SourceHeight
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if req.MaxWidth > 0 && w > req.MaxWidth {
		w, h = req.MaxWidth, int(math.Round(float64(h)*float64(req.MaxWidth)/float64(w)/2))*2
	}
	if req.MaxHeight > 0 {
		w, h = int(math.Round(float64(w)*float64(req.MaxHeight)/float64(h)/2))*2, req.MaxHeight
	}
	return w, h
}

// bitmapGraph is the filter graph of a picture subtitle burn-in: the
// subtitle pictures are scaled to the size of the output picture, then
// overlaid on it. A canvas of another shape than the picture is fitted to its
// height and cropped or padded to its size.
func bitmapGraph(req Request, subInput int) string {
	sub := req.Subtitle
	w, h := req.outputSize()
	main := "[0:v:0]" + burnVideoFilter(req) + "[main]"
	if w == 0 {
		return fmt.Sprintf("[%d:%d]null[sub];%s;[main][sub]overlay=eof_action=pass:repeatlast=0[v]", subInput, sub.StreamIndex, main)
	}
	var s string
	if sub.Width > 0 && sub.Height > 0 && sub.Width*h == sub.Height*w {
		s = fmt.Sprintf("scale,scale=%d:%d:fast_bilinear", w, h)
	} else {
		s = fmt.Sprintf(`scale,scale=-1:%d:fast_bilinear,crop,pad=max(%d\,iw):max(%d\,ih):(ow-iw)/2:(oh-ih)/2:black@0,crop=%d:%d`, h, w, h, w, h)
	}
	return fmt.Sprintf("[%d:%d]%s[sub];%s;[main][sub]overlay=eof_action=pass:repeatlast=0[v]", subInput, sub.StreamIndex, s, main)
}

// burnVideoFilter is the part of the video filter chain a burn-in has before
// the subtitles: the colour tags, the scaling and the pixel format. The
// renderer of a text track comes after them, so the text is laid out for the
// picture the viewer gets.
func burnVideoFilter(req Request) string {
	chain := []string{"setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709"}
	if req.MaxWidth > 0 {
		chain = append(chain, fmt.Sprintf("scale='min(iw,%d)':-2", req.MaxWidth))
	}
	if req.MaxHeight > 0 {
		chain = append(chain, fmt.Sprintf("scale=-2:%d", req.MaxHeight))
	}
	chain = append(chain, "format=yuv420p")
	return strings.Join(chain, ",")
}

func canvasArgs(sub *Subtitle) []string {
	if sub != nil && sub.Bitmap && sub.Width > 0 && sub.Height > 0 {
		return []string{"-canvas_size", strconv.Itoa(sub.Width) + "x" + strconv.Itoa(sub.Height)}
	}
	return nil
}

// resolveText prepares a text burn-in. An embedded track is extracted to a
// file of the job (ASS kept as ASS, anything else written as SubRip) with the
// font "Arial" of its styles replaced, and burned from there; an external file
// with a language in its name gets the encoding it is written in. When the
// extraction fails the track is read from the container instead.
func (c Config) resolveText(req *Request, jobDir string) {
	sub := req.Subtitle
	c.extractFonts(req, jobDir)
	if !sub.Embedded {
		if lang := sidecarLanguage(req.InputPath, sub.Path); lang != "" {
			if data, err := os.ReadFile(sub.Path); err == nil {
				sub.Charset = detectCharset(data)
			}
		}
		return
	}
	var codec string
	n := 0
	for _, s := range c.probeStreams(req.InputPath) {
		if s.CodecType != "subtitle" {
			continue
		}
		if n == sub.Index {
			codec = s.CodecName
			break
		}
		n++
	}
	if codec == "" {
		return
	}
	ext, enc := "srt", "srt"
	if codec == "ass" || codec == "ssa" {
		ext, enc = "ass", "ass"
	}
	out := filepath.Join(jobDir, "subtitle."+ext)
	cmd := exec.Command(c.ffmpegPath(), "-v", "error", "-y", "-i", req.InputPath,
		"-map", "0:s:"+strconv.Itoa(sub.Index), "-c:s", enc, out)
	if cmd.Run() != nil {
		return
	}
	if ext == "ass" {
		if data, err := os.ReadFile(out); err == nil {
			if fixed, ok := subtitle.ReplaceArialStyle(data); ok {
				_ = os.WriteFile(out, fixed, 0o644)
			}
		}
	}
	sub.Path, sub.Embedded, sub.Index = out, false, 0
}

// sidecarLanguage is the language the name of an external subtitle file gives.
func sidecarLanguage(videoPath, subPath string) string {
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	sc, ok := subtitle.ParseSidecar(base, filepath.Base(subPath))
	if !ok {
		return ""
	}
	return sc.Language
}

// extractFonts writes the attachments of the input file (the fonts a Matroska
// file carries for its ASS styles) to a directory of the job and has libass
// read fonts from there, as the reference does with the attachments of the
// file; without attachments the fonts directory stays as configured.
func (c Config) extractFonts(req *Request, jobDir string) {
	has := false
	for _, s := range c.probeStreams(req.InputPath) {
		if s.CodecType == "attachment" {
			has = true
			break
		}
	}
	if !has {
		return
	}
	dir := filepath.Join(jobDir, "fonts")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	cmd := exec.Command(c.ffmpegPath(), "-v", "error", "-dump_attachment:t", "", "-y", "-i", req.InputPath)
	cmd.Dir = dir
	_ = cmd.Run() // ffmpeg exits with an error: there is no output file to write
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		req.FontsDir = dir
	}
}
