package mediamtx

// PathsResponse mirrors the part of GET /v3/paths/list used by DronService.
// MediaMTX-specific response types remain in this package.
type PathsResponse struct {
	Items []Path `json:"items"`
}

type Path struct {
	Name          string   `json:"name"`
	Ready         bool     `json:"ready"`
	Available     bool     `json:"available"`
	Online        bool     `json:"online"`
	Readers       []Reader `json:"readers"`
	InboundBytes  int64    `json:"inboundBytes"`
	OutboundBytes int64    `json:"outboundBytes"`
}

type Reader struct{}

type ConfigPathsResponse struct {
	Items []ConfigPath `json:"items"`
}

type ConfigPath struct {
	Name           string `json:"name"`
	Source         string `json:"source"`
	SourceOnDemand bool   `json:"sourceOnDemand"`
	RunOnDemand    string `json:"runOnDemand"`
}

type PathConfigUpdate struct {
	Source                  string `json:"source"`
	SourceOnDemand          bool   `json:"sourceOnDemand"`
	RunOnDemand             string `json:"runOnDemand"`
	RunOnDemandRestart      bool   `json:"runOnDemandRestart"`
	RunOnDemandStartTimeout string `json:"runOnDemandStartTimeout,omitempty"`
	RunOnDemandCloseAfter   string `json:"runOnDemandCloseAfter,omitempty"`
}
