package normalizer

import (
	"math"
	"reflect"
	"testing"
)

func TestParseProbe(t *testing.T) {
	cases := []struct {
		name, stderr string
		want         Probe
	}{
		{"flac from a recorder", `Input #0, flac, from 'x.flac':
  Metadata:
    TITLE           : Gudstjeneste
    encoder         : Lavf61.7.100
    date            : 2026-10-04
  Duration: 01:32:10.20, start: 0.000000, bitrate: 1412 kb/s
  Stream #0:0: Audio: flac, 48000 Hz, stereo, s32 (24 bit)
Stream mapping:
  Stream #0:0 -> #0:0 (flac (native) -> pcm_s16le (native))
Output #0, null, to 'pipe:':
  Metadata:
    encoder         : Lavf61.7.100
`, Probe{Container: "flac", Codec: "flac", SampleRate: 48000, Bits: 24, Channels: 2, Bitrate: 1412, Duration: 5530.2,
			Metadata: map[string]string{"title": "Gudstjeneste", "encoder": "Lavf61.7.100", "date": "2026-10-04"}}},
		{"wav", `Input #0, wav, from 'x.wav':
  Duration: 00:01:40.00, bitrate: 2304 kb/s
  Stream #0:0: Audio: pcm_s24le ([1][0][0][0] / 0x0001), 48000 Hz, 2 channels, s32 (24 bit), 2304 kb/s
`, Probe{Container: "wav", Codec: "pcm_s24le", SampleRate: 48000, Bits: 24, Channels: 2, Bitrate: 2304, Duration: 100}},
		{"16-bit mono wav, no bit note", `Input #0, wav, from 'x.wav':
  Duration: 00:00:30.00, bitrate: 768 kb/s
  Stream #0:0: Audio: pcm_s16le ([1][0][0][0] / 0x0001), 48000 Hz, mono, s16, 768 kb/s
`, Probe{Container: "wav", Codec: "pcm_s16le", SampleRate: 48000, Bits: 16, Channels: 1, Bitrate: 768, Duration: 30}},
		{"flac s16", `Input #0, flac, from 'x.flac':
  Duration: 00:00:30.00, start: 0.000000, bitrate: 400 kb/s
  Stream #0:0: Audio: flac, 8000 Hz, mono, s16
`, Probe{Container: "flac", Codec: "flac", SampleRate: 8000, Bits: 16, Channels: 1, Bitrate: 400, Duration: 30}},
	}
	for _, c := range cases {
		got, err := parseProbe(c.stderr)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	if _, err := parseProbe("Input #0, wav, from 'x':\n  Duration: 00:00:01.00\n"); err == nil {
		t.Error("no audio stream: want an error")
	}
}

func TestChannelVerdict(t *testing.T) {
	inf := math.Inf(-1)
	cases := []struct {
		c    ChannelCheck
		want ChannelVerdict
	}{
		{ChannelCheck{Channels: 1}, ChannelVerdict{Kind: "mono"}},
		{ChannelCheck{Channels: 6}, ChannelVerdict{Kind: "more"}},
		{ChannelCheck{Channels: 2, Left: -20, Right: -22, Side: -8}, ChannelVerdict{Kind: "stereo"}},
		{ChannelCheck{Channels: 2, Left: -20, Right: -20, Side: inf}, ChannelVerdict{Kind: "identical"}},
		{ChannelCheck{Channels: 2, Left: -20, Right: -20.1, Side: -45}, ChannelVerdict{Kind: "identical"}},
		{ChannelCheck{Channels: 2, Left: -20, Right: inf, Side: 0}, ChannelVerdict{Kind: "one-sided", Quiet: "right"}},
		{ChannelCheck{Channels: 2, Left: -60, Right: -20, Side: -1}, ChannelVerdict{Kind: "one-sided", Quiet: "left", By: 40}},
	}
	for _, c := range cases {
		if got := c.c.Verdict(); got != c.want {
			t.Errorf("%+v: got %+v, want %+v", c.c, got, c.want)
		}
	}
}
