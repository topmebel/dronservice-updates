package ipcamera

import "testing"

func TestParseUNVVideoStreamDetailInfos(t *testing.T) {
	mainStream, subStream := parseUNVVideoStreamDetailInfos([]byte(`{
		"Num": 2,
		"VideoStreamInfos": [{
			"ID": 0,
			"VideoEncodeInfo": {
				"Resolution": {"Width": 1280, "Height": 720},
				"FrameRate": 15,
				"BitRate": 1024
			}
		}, {
			"ID": 1,
			"VideoEncodeInfo": {
				"Resolution": {"Width": 640, "Height": 360},
				"FrameRate": 16,
				"BitRate": 512
			}
		}]
	}`))
	if mainStream != (VideoStream{Resolution: "1280x720", FPS: "15", BitrateKbps: 1024}) {
		t.Fatalf("main stream = %#v", mainStream)
	}
	if subStream != (VideoStream{Resolution: "640x360", FPS: "16", BitrateKbps: 512}) {
		t.Fatalf("sub stream = %#v", subStream)
	}
}

func TestParseUNVVideoStreamUsesVideoEncodeInfo(t *testing.T) {
	stream := parseUNVVideoStream([]byte(`{
		"ID": 0,
		"VideoEncodeInfo": {
			"Resolution": {"Width": 1920, "Height": 1080},
			"FrameRate": 25,
			"BitRate": 4096
		}
	}`))
	if stream.Resolution != "1920x1080" || stream.FPS != "25" || stream.BitrateKbps != 4096 {
		t.Fatalf("stream = %#v", stream)
	}
}

func TestParseUNVVideoStreamUsesFlatFields(t *testing.T) {
	stream := parseUNVVideoStream([]byte(`{
		"dwWidth": 640,
		"dwHeight": 360,
		"dwFrameRate": 15,
		"dwBitRate": 512
	}`))
	if stream.Resolution != "640x360" || stream.FPS != "15" || stream.BitrateKbps != 512 {
		t.Fatalf("stream = %#v", stream)
	}
}

func TestMergeVideoStreamPrefersConfiguredBitrate(t *testing.T) {
	merged := mergeVideoStream(
		VideoStream{Resolution: "1280x720", BitrateKbps: 2048},
		VideoStream{Resolution: "1280x720", FPS: "20"},
	)
	if merged.FPS != "20" || merged.BitrateKbps != 2048 {
		t.Fatalf("merged = %#v", merged)
	}
}
