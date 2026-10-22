package normalizer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Kind is what becomes of a raw recording of some type when it arrives.
type Kind int

const (
	// Kept: used as it is. FLAC, and compressed audio, which FLAC would only
	// make bigger.
	Kept Kind = iota
	// ToFLAC: other lossless audio is made into FLAC, checked to decode to the
	// same audio, and then removed.
	ToFLAC
	// FromVideo: a video's sound is copied out, unchanged, into Matroska audio
	// (.mka), checked to have the very same packets, and the video removed.
	// (Decoded, it may differ by the encoder's first few ms, which an MP4
	// hides and Matroska does not.)
	FromVideo
)

// inputTypes are the types a raw recording may come as. The order is the
// preference when a folder has more than one: what a conversion made comes
// before what it was made from.
var inputTypes = []struct {
	ext  string
	kind Kind
}{
	{".flac", Kept}, {".mka", Kept},
	{".wav", ToFLAC}, {".aiff", ToFLAC}, {".aif", ToFLAC},
	{".mp3", Kept}, {".m4a", Kept}, {".aac", Kept}, {".ogg", Kept}, {".opus", Kept}, {".wma", Kept}, {".caf", Kept},
	{".mp4", FromVideo}, {".mov", FromVideo}, {".mkv", FromVideo}, {".webm", FromVideo},
}

// InputTypes are the extensions a raw recording may have, lower case.
func InputTypes() []string {
	out := make([]string, len(inputTypes))
	for i, t := range inputTypes {
		out[i] = t.ext
	}
	return out
}

// KindOf is what becomes of the file name when it arrives; ok is false for a
// type that is not taken.
func KindOf(name string) (kind Kind, ok bool) {
	ext := strings.ToLower(filepath.Ext(name))
	for _, t := range inputTypes {
		if t.ext == ext {
			return t.kind, true
		}
	}
	return Kept, false
}

// ConvertsTo is the file a ToFLAC or FromVideo recording becomes (path with
// .flac or .mka), or "" for one that is kept.
func ConvertsTo(path string) string {
	kind, _ := KindOf(path)
	base := strings.TrimSuffix(path, filepath.Ext(path))
	switch kind {
	case ToFLAC:
		return base + ".flac"
	case FromVideo:
		return base + ".mka"
	}
	return ""
}

// Convert makes dst (ConvertsTo(src)) from src and leaves src alone: the
// audio made into FLAC (tags kept, so a recording date survives), or a
// video's sound copied out. dst appears only when complete and checked to
// have the same audio as src. progress, if set, gets the seconds done.
func (f FFmpeg) Convert(ctx context.Context, src, dst string, progress func(seconds float64)) error {
	kind, _ := KindOf(src)
	codec := map[Kind]string{ToFLAC: "flac", FromVideo: "copy"}[kind]
	if codec == "" {
		return fmt.Errorf("%s is used as it is, not converted", filepath.Base(src))
	}
	if err := f.RunToProgress(ctx, dst, progress, "-i", src, "-map", "0:a:0", "-map_metadata", "0", "-c:a", codec); err != nil {
		return err
	}
	same, err := f.SameAudio(ctx, src, dst)
	if kind == FromVideo {
		same, err = f.samePackets(ctx, src, dst)
	}
	if err == nil && !same {
		err = fmt.Errorf("%s does not have exactly the audio of %s", filepath.Base(dst), filepath.Base(src))
	}
	if err != nil {
		Remove(dst)
	}
	return err
}

// samePackets reports whether the first audio streams of a and b are made of
// the same bytes, packet for packet.
func (f FFmpeg) samePackets(ctx context.Context, a, b string) (bool, error) {
	var sums [2]string
	for i, path := range []string{a, b} {
		res, err := f.Run(ctx, "-v", "error", "-i", path, "-map", "0:a:0", "-c", "copy", "-f", "hash", "-hash", "md5", "-")
		if err != nil {
			return false, err
		}
		m := reMD5.FindStringSubmatch(res.Stdout + res.Stderr)
		if m == nil {
			return false, fmt.Errorf("no MD5 found in ffmpeg output")
		}
		sums[i] = m[1]
	}
	return sums[0] == sums[1], nil
}
