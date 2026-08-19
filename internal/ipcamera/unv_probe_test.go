package ipcamera

import (
	"net"
	"strings"
	"testing"
)

func TestParseUNVProbeMatchExtractsCameraDetails(t *testing.T) {
	const messageID = "urn:uuid:9fa625e1-4c7b-4e61-86c8-9f59af591234"
	payload := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
 xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing"
 xmlns:tns="http://www.uniview.com/Ver10/Device/wsdl">
 <s:Header>
  <a:Action>http://www.uniview.com/Ver10/Device/wsdl/UniviewProbeMatches</a:Action>
  <a:RelatesTo>` + messageID + `</a:RelatesTo>
 </s:Header>
 <s:Body><tns:UniviewProbeMatches>
  <tns:Scopes>
   onvif://www.onvif.org/type/IPC
   onvif://www.onvif.org/manufacturer/UNIVIEW
   onvif://www.onvif.org/version/DIPC-B1232.9.3.C00010.251208
   onvif://www.onvif.org/serial/210235UEC3Q263000129
   onvif://www.onvif.org/macaddr/88263f8d6525
   onvif://www.onvif.org/hardware/IPC2124LE-ADF28KM-H
   onvif://www.onvif.org/name/IPC2124LE-ADF28KM-H
  </tns:Scopes>
  <tns:XAddrs>http://192.168.4.106:8080/LAPI/V1.0/System/DeviceInfo</tns:XAddrs>
 </tns:UniviewProbeMatches></s:Body>
</s:Envelope>`)

	device, err := ParseUNVProbeMatch(payload, &net.UDPAddr{IP: net.ParseIP("192.168.4.200"), Port: 45678}, "eth0", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if device.Model != "IPC2124LE-ADF28KM-H" || device.DeviceName != "IPC2124LE-ADF28KM-H" {
		t.Fatalf("model/name not parsed: %+v", device)
	}
	if device.Manufacturer != "UNIVIEW" || device.DeviceType != "IPC" {
		t.Fatalf("manufacturer/type not parsed: %+v", device)
	}
	if device.SerialNumber != "210235UEC3Q263000129" || device.FirmwareVersion != "DIPC-B1232.9.3.C00010.251208" {
		t.Fatalf("serial/firmware not parsed: %+v", device)
	}
	if device.MAC != "88:26:3f:8d:65:25" || device.Confidence != "high" {
		t.Fatalf("MAC/confidence not parsed: %+v", device)
	}
	if !device.IP.Equal(net.ParseIP("192.168.4.106")) || device.HTTPPort != 8080 {
		t.Fatalf("IP/HTTP port not parsed: %+v", device)
	}
}

func TestBuildUNVProbeUsesConfirmedElementPrefix(t *testing.T) {
	payload := string(buildUNVProbe("uuid:test"))
	if !strings.Contains(payload, `xmlns:tns="http://www.uniview.com/Ver10/Device/wsdl"`) || !strings.Contains(payload, `<tns:UniviewProbe/>`) {
		t.Fatalf("unexpected UNV probe XML: %s", payload)
	}
}

func TestParseUNVProbeMatchUsesDefaultHTTPPort(t *testing.T) {
	const messageID = "urn:uuid:test"
	payload := []byte(`<Envelope><Action>x/UniviewProbeMatches</Action><RelatesTo>` + messageID + `</RelatesTo><Scopes>onvif://www.onvif.org/hardware/IPC</Scopes><XAddrs>http://192.168.4.107/path</XAddrs></Envelope>`)
	device, err := ParseUNVProbeMatch(payload, nil, "eth0", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if device.HTTPPort != 80 {
		t.Fatalf("HTTP port = %d", device.HTTPPort)
	}
}

func TestParseUNVProbeMatchAcceptsFirmwareProbeAction(t *testing.T) {
	const messageID = "urn:uuid:firmware-probe-action"
	payload := []byte(`<Envelope><Action>` + unvProbeAction + `</Action><RelatesTo>` + messageID + `</RelatesTo><Scopes>onvif://www.onvif.org/macaddr/88263f7e7dda onvif://www.onvif.org/hardware/IPC3614LE</Scopes><XAddrs>http://192.168.4.107/</XAddrs></Envelope>`)
	device, err := ParseUNVProbeMatch(payload, nil, "eth0", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if device.MAC != "88:26:3f:7e:7d:da" || !device.IP.Equal(net.ParseIP("192.168.4.107")) || device.Model != "IPC3614LE" {
		t.Fatalf("device = %+v", device)
	}
}

func TestParseUNVProbeMatchAcceptsFirmwareResponseWithoutRelatesTo(t *testing.T) {
	payload := []byte(`<Envelope><Action>` + unvProbeAction + `</Action><Scopes>onvif://www.onvif.org/macaddr/88263f7e7dda onvif://www.onvif.org/hardware/IPC3614LE</Scopes><XAddrs>http://192.168.4.107/</XAddrs></Envelope>`)
	device, err := ParseUNVProbeMatch(payload, nil, "eth0", "urn:uuid:request")
	if err != nil {
		t.Fatal(err)
	}
	if device.MAC != "88:26:3f:7e:7d:da" || !device.IP.Equal(net.ParseIP("192.168.4.107")) {
		t.Fatalf("device = %+v", device)
	}
}

func TestParseUNVProbeMatchRejectsStandardResponseWithoutRelatesTo(t *testing.T) {
	payload := []byte(`<Envelope><Action>x/UniviewProbeMatches</Action><Scopes>onvif://www.onvif.org/macaddr/88263f7e7dda</Scopes><XAddrs>http://192.168.4.107/</XAddrs></Envelope>`)
	if _, err := ParseUNVProbeMatch(payload, nil, "eth0", "urn:uuid:request"); err == nil || !strings.Contains(err.Error(), "no RelatesTo") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseUNVProbeMatchRejectsUnrelatedResponse(t *testing.T) {
	payload := []byte(`<Envelope><Action>x/UniviewProbeMatches</Action><RelatesTo>urn:uuid:other</RelatesTo><XAddrs>http://192.168.4.107/</XAddrs></Envelope>`)
	if _, err := ParseUNVProbeMatch(payload, nil, "eth0", "urn:uuid:request"); err == nil || !strings.Contains(err.Error(), "RelatesTo") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUNVMessageIDAcceptsUUIDAndURNUUIDForms(t *testing.T) {
	if !sameUNVMessageID("uuid:9FA625E1-4C7B-4E61-86C8-9F59AF591234", "urn:uuid:9fa625e1-4c7b-4e61-86c8-9f59af591234") {
		t.Fatal("equivalent UNV MessageID forms did not match")
	}
	if sameUNVMessageID("uuid:one", "urn:uuid:two") {
		t.Fatal("different UNV MessageIDs matched")
	}
}

func TestUNVDeviceNameFromScopes(t *testing.T) {
	scopes := "onvif://www.onvif.org/type/IPC onvif://www.onvif.org/name/Front%20Gate%20Camera onvif://www.onvif.org/hardware/IPC2124LE"
	if got := unvDeviceNameFromScopes(scopes); got != "Front Gate Camera" {
		t.Fatalf("device name = %q", got)
	}
}

func TestParseUNVProbeMatchesReturnsEveryCamera(t *testing.T) {
	const messageID = "urn:uuid:multi-camera"
	payload := []byte(`<Envelope><Header><Action>x/UniviewProbeMatches</Action><RelatesTo>` + messageID + `</RelatesTo></Header><Body><UniviewProbeMatches>` +
		`<UniviewProbeMatch><Scopes>onvif://www.onvif.org/macaddr/88263f8d6525 onvif://www.onvif.org/hardware/IPC2124LE-A</Scopes><XAddrs>http://192.168.4.106/</XAddrs></UniviewProbeMatch>` +
		`<UniviewProbeMatch><Scopes>onvif://www.onvif.org/macaddr/88263f7e7dda onvif://www.onvif.org/hardware/IPC2124LE-B</Scopes><XAddrs>http://192.168.4.107/</XAddrs></UniviewProbeMatch>` +
		`</UniviewProbeMatches></Body></Envelope>`)
	devices, err := ParseUNVProbeMatches(payload, nil, "eth0", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("devices count = %d: %+v", len(devices), devices)
	}
	models := map[string]string{}
	for _, device := range devices {
		models[device.MAC] = device.Model
	}
	if models["88:26:3f:8d:65:25"] != "IPC2124LE-A" || models["88:26:3f:7e:7d:da"] != "IPC2124LE-B" {
		t.Fatalf("models = %#v", models)
	}
}

func TestUNVModelFallsBackToModelLikeDeviceName(t *testing.T) {
	const messageID = "urn:uuid:name-model"
	payload := []byte(`<Envelope><Action>x/UniviewProbeMatches</Action><RelatesTo>` + messageID + `</RelatesTo><Scopes>onvif://www.onvif.org/macaddr/88263f7e7dda onvif://www.onvif.org/name/IPC3614LE-ADF28K</Scopes><XAddrs>http://192.168.4.107/</XAddrs></Envelope>`)
	device, err := ParseUNVProbeMatch(payload, nil, "eth0", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if device.Model != "IPC3614LE-ADF28K" {
		t.Fatalf("model = %q", device.Model)
	}
}
