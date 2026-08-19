package ipcamera

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

type unvLAPIClient struct {
	digest *dahuaCGIClient
}

type unvDeviceInfo struct {
	DeviceName      string `json:"DeviceName"`
	DeviceModel     string `json:"DeviceModel"`
	PrototypeName   string `json:"PrototypeName"`
	SerialNumber    string `json:"SerialNumber"`
	FirmwareVersion string `json:"FirmwareVersion"`
}

func newUNVLAPIClient() *unvLAPIClient {
	return &unvLAPIClient{digest: newDahuaCGIClient()}
}

func (c *unvLAPIClient) DeviceInfo(ctx context.Context, address string, port uint16, username, password string) (unvDeviceInfo, error) {
	target := dahuaHTTPBase(address, port) + "/LAPI/V1.0/System/DeviceInfo"
	body, err := c.digest.getDigest(ctx, target, username, password)
	if err != nil {
		return unvDeviceInfo{}, fmt.Errorf("read UNV device information: %w", err)
	}
	var response struct {
		Response struct {
			ResponseCode int             `json:"ResponseCode"`
			ResponseText string          `json:"ResponseString"`
			Data         json.RawMessage `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return unvDeviceInfo{}, fmt.Errorf("decode UNV device information: %w", err)
	}
	if response.Response.ResponseCode != 0 {
		return unvDeviceInfo{}, fmt.Errorf("read UNV device information: camera returned %q", response.Response.ResponseText)
	}
	var info unvDeviceInfo
	if err := json.Unmarshal(response.Response.Data, &info); err != nil {
		return unvDeviceInfo{}, fmt.Errorf("decode UNV device information data: %w", err)
	}
	if info.DeviceModel == "" && info.PrototypeName == "" && info.DeviceName == "" {
		return unvDeviceInfo{}, fmt.Errorf("read UNV device information: response has no device identity")
	}
	return info, nil
}

func (c *unvLAPIClient) VideoStreams(ctx context.Context, address string, port uint16, username, password string) (VideoStream, VideoStream, error) {
	main, sub, detailErr := c.videoStreamsDetail(ctx, address, port, username, password)
	if main != (VideoStream{}) || sub != (VideoStream{}) {
		return main, sub, nil
	}
	main, mainErr := c.videoStream(ctx, address, port, username, password, 0)
	sub, subErr := c.videoStream(ctx, address, port, username, password, 1)
	if main == (VideoStream{}) && sub == (VideoStream{}) {
		return VideoStream{}, VideoStream{}, fmt.Errorf("read UNV video streams: detail: %v; main: %v; sub: %v", detailErr, mainErr, subErr)
	}
	return main, sub, nil
}

func (c *unvLAPIClient) videoStreamsDetail(ctx context.Context, address string, port uint16, username, password string) (VideoStream, VideoStream, error) {
	target := dahuaHTTPBase(address, port) + "/LAPI/V1.0/Channels/0/Media/Video/Streams/DetailInfos"
	body, err := c.digest.getDigest(ctx, target, username, password)
	if err != nil {
		return VideoStream{}, VideoStream{}, err
	}
	var response struct {
		Response struct {
			ResponseCode int             `json:"ResponseCode"`
			ResponseText string          `json:"ResponseString"`
			Data         json.RawMessage `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return VideoStream{}, VideoStream{}, fmt.Errorf("decode UNV video stream details: %w", err)
	}
	if response.Response.ResponseCode != 0 {
		return VideoStream{}, VideoStream{}, fmt.Errorf("read UNV video stream details: camera returned %q", response.Response.ResponseText)
	}
	main, sub := parseUNVVideoStreamDetailInfos(response.Response.Data)
	if main == (VideoStream{}) && sub == (VideoStream{}) {
		return VideoStream{}, VideoStream{}, fmt.Errorf("read UNV video stream details: response has no supported stream fields")
	}
	return main, sub, nil
}

func parseUNVVideoStreamDetailInfos(data json.RawMessage) (VideoStream, VideoStream) {
	if len(data) == 0 || string(data) == "null" {
		return VideoStream{}, VideoStream{}
	}
	var details struct {
		VideoStreamInfos []struct {
			ID              int             `json:"ID"`
			VideoEncodeInfo json.RawMessage `json:"VideoEncodeInfo"`
		} `json:"VideoStreamInfos"`
	}
	if err := json.Unmarshal(data, &details); err != nil {
		return VideoStream{}, VideoStream{}
	}
	var mainStream, subStream VideoStream
	for index, info := range details.VideoStreamInfos {
		stream := videoStreamFromUNVFields(info.VideoEncodeInfo)
		if stream == (VideoStream{}) {
			continue
		}
		switch info.ID {
		case 0:
			mainStream = stream
		case 1:
			subStream = stream
		default:
			if index == 0 && mainStream == (VideoStream{}) {
				mainStream = stream
			} else if subStream == (VideoStream{}) {
				subStream = stream
			}
		}
	}
	return mainStream, subStream
}

func (c *unvLAPIClient) videoStream(ctx context.Context, address string, port uint16, username, password string, streamID int) (VideoStream, error) {
	target := fmt.Sprintf("%s/LAPI/V1.0/Channels/0/Media/Video/Streams/%d", dahuaHTTPBase(address, port), streamID)
	body, err := c.digest.getDigest(ctx, target, username, password)
	if err != nil {
		return VideoStream{}, err
	}
	var response struct {
		Response struct {
			ResponseCode int             `json:"ResponseCode"`
			ResponseText string          `json:"ResponseString"`
			Data         json.RawMessage `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return VideoStream{}, fmt.Errorf("decode UNV video stream: %w", err)
	}
	if response.Response.ResponseCode != 0 {
		return VideoStream{}, fmt.Errorf("read UNV video stream: camera returned %q", response.Response.ResponseText)
	}
	stream := parseUNVVideoStream(response.Response.Data)
	if stream == (VideoStream{}) {
		return VideoStream{}, fmt.Errorf("read UNV video stream: response has no supported stream fields")
	}
	return stream, nil
}

func parseUNVVideoStream(data json.RawMessage) VideoStream {
	if len(data) == 0 {
		return VideoStream{}
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return VideoStream{}
	}
	candidates := []json.RawMessage{data}
	for _, key := range []string{"VideoEncodeInfo", "EncodeInfo"} {
		if raw, ok := root[key]; ok {
			candidates = append(candidates, raw)
		}
	}
	for _, raw := range candidates {
		if stream := videoStreamFromUNVFields(raw); stream != (VideoStream{}) {
			return stream
		}
	}
	return VideoStream{}
}

func videoStreamFromUNVFields(raw json.RawMessage) VideoStream {
	var fields struct {
		Width       uint32 `json:"Width"`
		Height      uint32 `json:"Height"`
		BitRate     uint32 `json:"BitRate"`
		FrameRate   uint32 `json:"FrameRate"`
		DwWidth     uint32 `json:"dwWidth"`
		DwHeight    uint32 `json:"dwHeight"`
		DwBitRate   uint32 `json:"dwBitRate"`
		DwFrameRate uint32 `json:"dwFrameRate"`
		Resolution  struct {
			Width  uint32 `json:"Width"`
			Height uint32 `json:"Height"`
		} `json:"Resolution"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return VideoStream{}
	}
	width := fields.Resolution.Width
	height := fields.Resolution.Height
	if width == 0 {
		width = fields.Width
	}
	if width == 0 {
		width = fields.DwWidth
	}
	if height == 0 {
		height = fields.Height
	}
	if height == 0 {
		height = fields.DwHeight
	}
	bitrate := fields.BitRate
	if bitrate == 0 {
		bitrate = fields.DwBitRate
	}
	frameRate := fields.FrameRate
	if frameRate == 0 {
		frameRate = fields.DwFrameRate
	}
	stream := VideoStream{}
	if width > 0 && height > 0 {
		stream.Resolution = fmt.Sprintf("%dx%d", width, height)
	}
	if frameRate > 0 {
		stream.FPS = strconv.FormatUint(uint64(frameRate), 10)
	}
	if bitrate > 0 {
		stream.BitrateKbps = bitrate
	}
	return stream
}

func mergeVideoStream(primary, fallback VideoStream) VideoStream {
	merged := primary
	if merged.Resolution == "" {
		merged.Resolution = fallback.Resolution
	}
	if merged.FPS == "" {
		merged.FPS = fallback.FPS
	}
	if merged.BitrateKbps == 0 {
		merged.BitrateKbps = fallback.BitrateKbps
	}
	return merged
}
