package ipcamera

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrDahuaCredentials            = errors.New("Dahua credentials rejected")
	ErrDahuaUnavailable            = errors.New("Dahua camera unavailable")
	ErrDahuaInitializationRequired = errors.New("Dahua camera initialization required")
)

type dahuaCGIClient struct{ http *http.Client }

func newDahuaCGIClient() *dahuaCGIClient {
	return &dahuaCGIClient{http: &http.Client{Timeout: 8 * time.Second}}
}

func (c *dahuaCGIClient) ChangeIPv4(ctx context.Context, address string, port uint16, username, password, newAddress, subnet, gateway string) error {
	base := dahuaHTTPBase(address, port)
	check := base + "/cgi-bin/configManager.cgi?action=getConfig&name=Network"
	networkConfig, err := c.getDigest(ctx, check, username, password)
	if err != nil {
		return err
	}
	fieldPrefix, err := dahuaNetworkFieldPrefix(networkConfig)
	if err != nil {
		return err
	}
	values := url.Values{"action": {"setConfig"}, fieldPrefix + ".DhcpEnable": {"false"}, fieldPrefix + ".IPAddress": {newAddress}}
	if subnet != "" {
		values.Set(fieldPrefix+".SubnetMask", subnet)
	}
	if gateway != "" {
		values.Set(fieldPrefix+".DefaultGateway", gateway)
	}
	body, err := c.getDigest(ctx, base+"/cgi-bin/configManager.cgi?"+values.Encode(), username, password)
	if err != nil {
		return fmt.Errorf("change Dahua IP address: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(body), "OK") {
		return fmt.Errorf("change Dahua IP address: camera returned %q", strings.TrimSpace(body))
	}
	return nil
}

func dahuaNetworkFieldPrefix(body string) (string, error) {
	for _, prefix := range []string{"Network.eth0[0]", "Network.eth0"} {
		if strings.Contains(body, prefix+".") {
			return prefix, nil
		}
	}
	return "", errors.New("Dahua Network response has no supported eth0 fields")
}

func (c *dahuaCGIClient) VideoStreams(ctx context.Context, address string, port uint16, username, password string) (VideoStream, VideoStream, error) {
	values := url.Values{"action": {"getConfig"}, "name": {"Encode"}}
	body, err := c.getDigest(ctx, dahuaHTTPBase(address, port)+"/cgi-bin/configManager.cgi?"+values.Encode(), username, password)
	if err != nil {
		return VideoStream{}, VideoStream{}, fmt.Errorf("read Dahua video configuration: %w", err)
	}
	mainStream, subStream := parseDahuaVideoStreams(body)
	if mainStream == (VideoStream{}) && subStream == (VideoStream{}) {
		return VideoStream{}, VideoStream{}, fmt.Errorf("read Dahua video configuration: Encode response has no supported stream fields")
	}
	return mainStream, subStream, nil
}

func dahuaHTTPBase(address string, port uint16) string {
	base := "http://" + address
	if port != 0 && port != 80 {
		base += fmt.Sprintf(":%d", port)
	}
	return base
}

var dahuaVideoSetting = regexp.MustCompile(`(?i)^(?:table\.)?Encode\[0\]\.(MainFormat|ExtraFormat)\[0\]\.Video\.(resolution|width|height|fps|bitrate)$`)

func parseDahuaVideoStreams(body string) (VideoStream, VideoStream) {
	var mainStream, subStream VideoStream
	type dimensions struct{ width, height string }
	streamDimensions := map[*VideoStream]*dimensions{
		&mainStream: {},
		&subStream:  {},
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		match := dahuaVideoSetting.FindStringSubmatch(strings.TrimSpace(key))
		if len(match) != 3 {
			continue
		}
		stream := &mainStream
		if strings.EqualFold(match[1], "ExtraFormat") {
			stream = &subStream
		}
		switch strings.ToLower(match[2]) {
		case "resolution":
			stream.Resolution = strings.TrimSpace(value)
		case "width":
			streamDimensions[stream].width = strings.TrimSpace(value)
		case "height":
			streamDimensions[stream].height = strings.TrimSpace(value)
		case "fps":
			stream.FPS = strings.TrimSpace(value)
		case "bitrate":
			bitrate, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
			if err == nil {
				stream.BitrateKbps = uint32(bitrate)
			}
		}
	}
	for stream, dimensions := range streamDimensions {
		if stream.Resolution == "" && dimensions.width != "" && dimensions.height != "" {
			stream.Resolution = dimensions.width + "x" + dimensions.height
		}
	}
	return mainStream, subStream
}

func (c *dahuaCGIClient) getDigest(ctx context.Context, target, username, password string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDahuaUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return readCGIResponse(resp)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	params := parseDigestChallenge(challenge)
	if params["realm"] == "" || params["nonce"] == "" {
		return "", ErrDahuaCredentials
	}
	parsed, _ := url.Parse(target)
	uri := parsed.RequestURI()
	nc, cnonce := "00000001", "dronservice"
	ha1 := md5hex(username + ":" + params["realm"] + ":" + password)
	ha2 := md5hex("GET:" + uri)
	response := md5hex(ha1 + ":" + params["nonce"] + ":" + ha2)
	auth := fmt.Sprintf(`Digest username=%q, realm=%q, nonce=%q, uri=%q, response=%q`, username, params["realm"], params["nonce"], uri, response)
	if strings.Contains(params["qop"], "auth") {
		response = md5hex(ha1 + ":" + params["nonce"] + ":" + nc + ":" + cnonce + ":auth:" + ha2)
		auth = fmt.Sprintf(`Digest username=%q, realm=%q, nonce=%q, uri=%q, response=%q, qop=auth, nc=%s, cnonce=%q`, username, params["realm"], params["nonce"], uri, response, nc, cnonce)
	}
	if opaque := params["opaque"]; opaque != "" {
		auth += fmt.Sprintf(`, opaque=%q`, opaque)
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	req.Header.Set("Authorization", auth)
	resp, err = c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDahuaUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrDahuaCredentials
	}
	return readCGIResponse(resp)
}

func readCGIResponse(resp *http.Response) (string, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Dahua CGI returned HTTP %d", resp.StatusCode)
	}
	return string(body), nil
}

var digestPair = regexp.MustCompile(`([a-zA-Z]+)=(?:"([^"]*)"|([^, ]+))`)

func parseDigestChallenge(value string) map[string]string {
	out := map[string]string{}
	for _, m := range digestPair.FindAllStringSubmatch(value, -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		out[strings.ToLower(m[1])] = v
	}
	return out
}
func md5hex(value string) string { sum := md5.Sum([]byte(value)); return hex.EncodeToString(sum[:]) }
