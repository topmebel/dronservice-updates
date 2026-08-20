package mavlink

import "time"

type LinkStatus struct {
	Connected   bool      `json:"connected"`
	LastSeenAt  time.Time `json:"lastSeenAt,omitempty"`
	SystemID    uint8     `json:"systemId,omitempty"`
	ComponentID uint8     `json:"componentId,omitempty"`
}

type FlightStatus struct {
	Armed        bool   `json:"armed"`
	CustomMode   uint32 `json:"customMode"`
	SystemStatus string `json:"systemStatus,omitempty"`
	Autopilot    string `json:"autopilot,omitempty"`
	VehicleType  string `json:"vehicleType,omitempty"`
}

type PositionStatus struct {
	Latitude       float64 `json:"latitude,omitempty"`
	Longitude      float64 `json:"longitude,omitempty"`
	AltitudeMSL    float64 `json:"altitudeMsl,omitempty"`
	AltitudeRel    float64 `json:"altitudeRelative,omitempty"`
	Heading        float64 `json:"heading,omitempty"`
	Satellites     uint8   `json:"satellites,omitempty"`
	GpsFixType     uint8   `json:"gpsFixType,omitempty"`
	GpsFixTypeName string  `json:"gpsFixTypeName,omitempty"`
}

type AttitudeStatus struct {
	RollDeg  float64 `json:"rollDeg,omitempty"`
	PitchDeg float64 `json:"pitchDeg,omitempty"`
	YawDeg   float64 `json:"yawDeg,omitempty"`
}

type BatteryStatus struct {
	Voltage   float64 `json:"voltage,omitempty"`
	Current   float64 `json:"current,omitempty"`
	Remaining int8    `json:"remaining,omitempty"`
}

type HUDStatus struct {
	Airspeed    float64 `json:"airspeed,omitempty"`
	Groundspeed float64 `json:"groundspeed,omitempty"`
	Climb       float64 `json:"climb,omitempty"`
	Throttle    uint16  `json:"throttle,omitempty"`
}

type Snapshot struct {
	Enabled    bool            `json:"enabled"`
	Config     Config          `json:"config"`
	Link       LinkStatus      `json:"link"`
	Flight     *FlightStatus   `json:"flight,omitempty"`
	Position   *PositionStatus `json:"position,omitempty"`
	Attitude   *AttitudeStatus `json:"attitude,omitempty"`
	Battery    *BatteryStatus  `json:"battery,omitempty"`
	HUD        *HUDStatus      `json:"hud,omitempty"`
	StatusText string          `json:"statusText,omitempty"`
	StatusAt   time.Time       `json:"statusAt,omitempty"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}
