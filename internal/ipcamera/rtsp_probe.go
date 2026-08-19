package ipcamera

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func probeRTSPVideoStream(ctx context.Context, source string) (VideoStream, error) {
	command := exec.CommandContext(
		ctx,
		"ffprobe",
		"-v", "error",
		"-rtsp_transport", "tcp",
		"-show_entries", "stream=codec_type,width,height,r_frame_rate,avg_frame_rate,bit_rate",
		"-of", "json",
		source,
	)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return VideoStream{}, fmt.Errorf("probe RTSP stream: %w", ctx.Err())
		}
		return VideoStream{}, fmt.Errorf("probe RTSP stream: ffprobe failed: %w", err)
	}
	stream, err := parseFFprobeVideoStream(output)
	if err != nil {
		return VideoStream{}, fmt.Errorf("probe RTSP stream: %w", err)
	}
	return stream, nil
}

func parseFFprobeVideoStream(payload []byte) (VideoStream, error) {
	var result struct {
		Streams []struct {
			CodecType    string `json:"codec_type"`
			Width        uint32 `json:"width"`
			Height       uint32 `json:"height"`
			FrameRate    string `json:"r_frame_rate"`
			AvgFrameRate string `json:"avg_frame_rate"`
			Bitrate      string `json:"bit_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return VideoStream{}, fmt.Errorf("decode ffprobe response: %w", err)
	}
	for _, candidate := range result.Streams {
		if candidate.CodecType != "video" && (candidate.Width == 0 || candidate.Height == 0) {
			continue
		}
		stream := VideoStream{}
		if candidate.Width != 0 && candidate.Height != 0 {
			stream.Resolution = fmt.Sprintf("%dx%d", candidate.Width, candidate.Height)
		}
		stream.FPS = formatFrameRate(candidate.AvgFrameRate)
		if stream.FPS == "" {
			stream.FPS = formatFrameRate(candidate.FrameRate)
		}
		if bitrate, err := strconv.ParseUint(candidate.Bitrate, 10, 64); err == nil {
			stream.BitrateKbps = uint32((bitrate + 500) / 1000)
		}
		if stream == (VideoStream{}) {
			continue
		}
		return stream, nil
	}
	return VideoStream{}, fmt.Errorf("ffprobe response has no video stream")
}

func formatFrameRate(value string) string {
	value = strings.TrimSpace(value)
	numerator, denominator, ok := strings.Cut(value, "/")
	if !ok {
		rate, err := strconv.ParseFloat(value, 64)
		if err != nil || rate <= 0 {
			return ""
		}
		return strconv.FormatFloat(rate, 'f', -1, 64)
	}
	left, leftErr := strconv.ParseFloat(numerator, 64)
	right, rightErr := strconv.ParseFloat(denominator, 64)
	if leftErr != nil || rightErr != nil || left <= 0 || right <= 0 {
		return ""
	}
	formatted := strconv.FormatFloat(left/right, 'f', 2, 64)
	return strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
}
