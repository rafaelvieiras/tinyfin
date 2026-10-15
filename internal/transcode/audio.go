package transcode

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

// AudioRequest describes one audio-only encode streamed to the caller (the
// http protocol of /Audio/{id}/stream and /universal): the output is not a
// job on disk, it is the stdout of the process.
type AudioRequest struct {
	InputPath string
	// Encoder is the ffmpeg encoder (libmp3lame, aac, libopus, flac, ...).
	Encoder string
	// Format is the ffmpeg muxer (mp3, adts, ogg, flac, mp4, mpegts, ...).
	Format string
	// Bitrate in bits/s, 0 = the encoder default (lossless encoders).
	Bitrate int
	// Fragmented asks for the movflags a non-seekable mp4 output needs.
	Fragmented bool
	// Channels caps the channel count when > 0.
	Channels int
	// StreamIndex is the input stream to encode, nil = first audio stream.
	StreamIndex *int
}

// AudioArgs builds the ffmpeg arguments (after the binary name) of req.
func AudioArgs(req AudioRequest) []string {
	args := []string{"-v", "error", "-nostdin", "-i", req.InputPath, "-vn", "-sn", "-dn"}
	if req.StreamIndex != nil {
		args = append(args, "-map", fmt.Sprintf("0:%d", *req.StreamIndex))
	} else {
		args = append(args, "-map", "0:a:0")
	}
	args = append(args, "-map_metadata", "-1", "-c:a", req.Encoder)
	if req.Encoder == "opus" || req.Encoder == "vorbis" {
		// the built-in encoders are marked experimental
		args = append(args, "-strict", "-2")
	}
	if req.Bitrate > 0 {
		args = append(args, "-b:a", strconv.Itoa(req.Bitrate))
	}
	if req.Channels > 0 {
		args = append(args, "-ac", strconv.Itoa(req.Channels))
	}
	if req.Fragmented {
		args = append(args, "-movflags", "frag_keyframe+empty_moov+default_base_moof")
	}
	return append(args, "-f", req.Format, "pipe:1")
}

// AudioStream is a running audio encode; Read yields the encoded bytes.
type AudioStream struct {
	cmd    *exec.Cmd
	out    io.ReadCloser
	stderr *bytes.Buffer
}

// StreamAudio starts the encode of req; ctx cancellation kills the process.
func (m *Manager) StreamAudio(ctx context.Context, req AudioRequest) (*AudioStream, error) {
	cmd := exec.CommandContext(ctx, m.cfg.ffmpegPath(), AudioArgs(req)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("transcode: start ffmpeg: %w", err)
	}
	return &AudioStream{cmd: cmd, out: out, stderr: &stderr}, nil
}

// Read implements io.Reader.
func (a *AudioStream) Read(p []byte) (int, error) { return a.out.Read(p) }

// Close ends the process (if still running) and reaps it.
func (a *AudioStream) Close() error {
	if a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
	return a.cmd.Wait()
}

// Stderr is what ffmpeg wrote to its error output so far.
func (a *AudioStream) Stderr() string { return a.stderr.String() }
