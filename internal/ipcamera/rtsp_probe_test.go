package ipcamera

import "testing"

func TestParseFFprobeVideoStream(t *testing.T) {
	payload := []byte(`{
		"streams": [
			{"codec_type":"audio","r_frame_rate":"0/0","avg_frame_rate":"0/0"},
			{"codec_type":"video","width":1280,"height":720,"r_frame_rate":"12/1","avg_frame_rate":"12000/1000","bit_rate":"2048000"}
		]
	}`)
	stream, err := parseFFprobeVideoStream(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := VideoStream{Resolution: "1280x720", FPS: "12", BitrateKbps: 2048}
	if stream != want {
		t.Fatalf("stream = %#v, want %#v", stream, want)
	}
}

func TestParseFFprobeVideoStreamFallsBackToNominalFrameRate(t *testing.T) {
	payload := []byte(`{"streams":[{"width":1920,"height":1080,"r_frame_rate":"30000/1001","avg_frame_rate":"0/0"}]}`)
	stream, err := parseFFprobeVideoStream(payload)
	if err != nil {
		t.Fatal(err)
	}
	if stream.FPS != "29.97" {
		t.Fatalf("FPS = %q, want 29.97", stream.FPS)
	}
}
