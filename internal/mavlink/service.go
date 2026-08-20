package mavlink

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/bluenviron/gomavlib/v3"
	"github.com/bluenviron/gomavlib/v3/pkg/dialects/common"
)

var telemetryMessageIDs = []uint32{
	(*common.MessageSysStatus)(nil).GetID(),
	(*common.MessageGpsRawInt)(nil).GetID(),
	(*common.MessageGlobalPositionInt)(nil).GetID(),
	(*common.MessageAttitude)(nil).GetID(),
	(*common.MessageVfrHud)(nil).GetID(),
	(*common.MessageBatteryStatus)(nil).GetID(),
}

type Service struct {
	mu       sync.RWMutex
	store    *Store
	config   Config
	snapshot Snapshot

	restart chan struct{}
	now     func() time.Time
}

func NewService(store *Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("MAVLink store is required")
	}
	config := DefaultConfig().Merge(store.Config())
	config = LoadConfigFromEnv(config)
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate MAVLink configuration: %w", err)
	}
	return &Service{
		store:   store,
		config:  config,
		restart: make(chan struct{}, 1),
		now:     time.Now,
	}, nil
}

func (s *Service) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotWithLinkStateLocked(s.now())
}

func (s *Service) UpdateConfig(config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if err := s.store.Save(config); err != nil {
		return err
	}
	s.mu.Lock()
	s.config = config
	s.snapshot.Enabled = config.Enabled
	s.snapshot.Config = config
	s.mu.Unlock()
	s.requestRestart()
	return nil
}

func (s *Service) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		cfg := s.Config()
		if !cfg.Enabled {
			s.setDisabledSnapshot(cfg)
			select {
			case <-ctx.Done():
				return
			case <-s.restart:
			case <-time.After(time.Second):
			}
			continue
		}
		if err := s.runSession(ctx, cfg); err != nil && ctx.Err() == nil {
			log.Printf("MAVLink session ended: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (s *Service) requestRestart() {
	select {
	case s.restart <- struct{}{}:
	default:
	}
}

func (s *Service) setDisabledSnapshot(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.snapshot = Snapshot{
		Enabled:   false,
		Config:    cfg,
		Link:      LinkStatus{Connected: false},
		UpdatedAt: now,
	}
}

func (s *Service) runSession(ctx context.Context, cfg Config) error {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-s.restart:
			cancel()
		}
	}()

	node := &gomavlib.Node{
		Endpoints: []gomavlib.EndpointConf{
			gomavlib.EndpointUDPServer{Address: cfg.UDPAddr},
		},
		Dialect:     common.Dialect,
		OutVersion:  gomavlib.V2,
		OutSystemID: cfg.OutSystemID,
	}
	if err := node.Initialize(); err != nil {
		return fmt.Errorf("initialize MAVLink node: %w", err)
	}
	defer node.Close()

	s.mu.Lock()
	s.snapshot.Enabled = true
	s.snapshot.Config = cfg
	s.mu.Unlock()

	var (
		targetSystem     = cfg.TargetSystemID
		streamsRequested bool
		lastChannel      *gomavlib.Channel
	)

	for {
		select {
		case <-sessionCtx.Done():
			return sessionCtx.Err()
		case evt, ok := <-node.Events():
			if !ok {
				return fmt.Errorf("MAVLink node stopped")
			}
			frameEvent, ok := evt.(*gomavlib.EventFrame)
			if !ok {
				continue
			}
			lastChannel = frameEvent.Channel
			systemID := frameEvent.SystemID()
			if targetSystem == 0 {
				switch msg := frameEvent.Message().(type) {
				case *common.MessageHeartbeat:
					if msg.Autopilot == common.MAV_AUTOPILOT_INVALID {
						continue
					}
					targetSystem = systemID
				default:
					continue
				}
			}
			if systemID != targetSystem {
				continue
			}
			if heartbeat, ok := frameEvent.Message().(*common.MessageHeartbeat); ok && !streamsRequested && lastChannel != nil {
				s.requestTelemetryStreams(node, lastChannel, targetSystem, cfg.MessageInterval)
				streamsRequested = true
				s.updateFromHeartbeat(heartbeat, systemID, frameEvent.ComponentID())
				continue
			}
			s.handleMessage(frameEvent.Message(), systemID, frameEvent.ComponentID())
		}
	}
}

func (s *Service) requestTelemetryStreams(node *gomavlib.Node, channel *gomavlib.Channel, targetSystem uint8, interval time.Duration) {
	intervalMicros := float32(interval.Microseconds())
	for _, messageID := range telemetryMessageIDs {
		command := &common.MessageCommandLong{
			TargetSystem:    targetSystem,
			TargetComponent: 0,
			Command:         common.MAV_CMD_SET_MESSAGE_INTERVAL,
			Param1:          float32(messageID),
			Param2:          intervalMicros,
		}
		if err := node.WriteMessageTo(channel, command); err != nil {
			log.Printf("request MAVLink message %d interval: %v", messageID, err)
		}
	}
}

func (s *Service) handleMessage(message any, systemID, componentID uint8) {
	now := s.now()
	switch msg := message.(type) {
	case *common.MessageHeartbeat:
		s.updateFromHeartbeat(msg, systemID, componentID)
	case *common.MessageSysStatus:
		s.mu.Lock()
		battery := s.ensureBatteryLocked()
		if msg.VoltageBattery != math.MaxUint16 {
			battery.Voltage = float64(msg.VoltageBattery) / 1000
		}
		if msg.CurrentBattery != -1 {
			battery.Current = float64(msg.CurrentBattery) / 100
		}
		if msg.BatteryRemaining != -1 {
			battery.Remaining = msg.BatteryRemaining
		}
		s.touchLocked(now, systemID, componentID)
		s.mu.Unlock()
	case *common.MessageGpsRawInt:
		s.mu.Lock()
		position := s.ensurePositionLocked()
		if msg.Lat != 0 || msg.Lon != 0 {
			position.Latitude = float64(msg.Lat) / 1e7
			position.Longitude = float64(msg.Lon) / 1e7
		}
		if msg.Alt != 0 {
			position.AltitudeMSL = float64(msg.Alt) / 1000
		}
		position.Satellites = msg.SatellitesVisible
		position.GpsFixType = uint8(msg.FixType)
		position.GpsFixTypeName = msg.FixType.String()
		s.touchLocked(now, systemID, componentID)
		s.mu.Unlock()
	case *common.MessageGlobalPositionInt:
		s.mu.Lock()
		position := s.ensurePositionLocked()
		if msg.Lat != 0 || msg.Lon != 0 {
			position.Latitude = float64(msg.Lat) / 1e7
			position.Longitude = float64(msg.Lon) / 1e7
		}
		position.AltitudeMSL = float64(msg.Alt) / 1000
		position.AltitudeRel = float64(msg.RelativeAlt) / 1000
		if msg.Hdg != math.MaxUint16 {
			position.Heading = float64(msg.Hdg) / 100
		}
		s.touchLocked(now, systemID, componentID)
		s.mu.Unlock()
	case *common.MessageAttitude:
		s.mu.Lock()
		attitude := s.ensureAttitudeLocked()
		attitude.RollDeg = radToDeg(msg.Roll)
		attitude.PitchDeg = radToDeg(msg.Pitch)
		attitude.YawDeg = radToDeg(msg.Yaw)
		s.touchLocked(now, systemID, componentID)
		s.mu.Unlock()
	case *common.MessageVfrHud:
		s.mu.Lock()
		hud := s.ensureHUDLocked()
		hud.Airspeed = float64(msg.Airspeed)
		hud.Groundspeed = float64(msg.Groundspeed)
		hud.Climb = float64(msg.Climb)
		hud.Throttle = msg.Throttle
		s.touchLocked(now, systemID, componentID)
		s.mu.Unlock()
	case *common.MessageBatteryStatus:
		s.mu.Lock()
		battery := s.ensureBatteryLocked()
		if voltage := batteryVoltageFromCells(msg.Voltages); voltage > 0 {
			battery.Voltage = voltage
		}
		if msg.CurrentBattery != -1 {
			battery.Current = float64(msg.CurrentBattery) / 100
		}
		if msg.BatteryRemaining != -1 {
			battery.Remaining = msg.BatteryRemaining
		}
		s.touchLocked(now, systemID, componentID)
		s.mu.Unlock()
	case *common.MessageStatustext:
		s.mu.Lock()
		s.snapshot.StatusText = msg.Text
		s.snapshot.StatusAt = now
		s.touchLocked(now, systemID, componentID)
		s.mu.Unlock()
	}
}

func (s *Service) updateFromHeartbeat(msg *common.MessageHeartbeat, systemID, componentID uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	flight := s.ensureFlightLocked()
	flight.Armed = msg.BaseMode&common.MAV_MODE_FLAG_SAFETY_ARMED != 0
	flight.CustomMode = msg.CustomMode
	flight.SystemStatus = msg.SystemStatus.String()
	flight.Autopilot = msg.Autopilot.String()
	flight.VehicleType = msg.Type.String()
	s.touchLocked(now, systemID, componentID)
}

func (s *Service) snapshotWithLinkStateLocked(now time.Time) Snapshot {
	snapshot := s.snapshot
	timeout := snapshot.Config.LinkTimeout
	if timeout <= 0 {
		timeout = DefaultConfig().LinkTimeout
	}
	if !snapshot.Link.LastSeenAt.IsZero() && now.Sub(snapshot.Link.LastSeenAt) <= timeout {
		snapshot.Link.Connected = true
	} else {
		snapshot.Link.Connected = false
	}
	snapshot.UpdatedAt = now
	return snapshot
}

func (s *Service) touchLocked(now time.Time, systemID, componentID uint8) {
	s.snapshot.Link.LastSeenAt = now
	s.snapshot.Link.SystemID = systemID
	s.snapshot.Link.ComponentID = componentID
	s.snapshot.UpdatedAt = now
}

func (s *Service) ensureFlightLocked() *FlightStatus {
	if s.snapshot.Flight == nil {
		s.snapshot.Flight = &FlightStatus{}
	}
	return s.snapshot.Flight
}

func (s *Service) ensurePositionLocked() *PositionStatus {
	if s.snapshot.Position == nil {
		s.snapshot.Position = &PositionStatus{}
	}
	return s.snapshot.Position
}

func (s *Service) ensureAttitudeLocked() *AttitudeStatus {
	if s.snapshot.Attitude == nil {
		s.snapshot.Attitude = &AttitudeStatus{}
	}
	return s.snapshot.Attitude
}

func (s *Service) ensureBatteryLocked() *BatteryStatus {
	if s.snapshot.Battery == nil {
		s.snapshot.Battery = &BatteryStatus{}
	}
	return s.snapshot.Battery
}

func (s *Service) ensureHUDLocked() *HUDStatus {
	if s.snapshot.HUD == nil {
		s.snapshot.HUD = &HUDStatus{}
	}
	return s.snapshot.HUD
}

func radToDeg(value float32) float64 {
	return float64(value) * 180 / math.Pi
}

func batteryVoltageFromCells(cells [10]uint16) float64 {
	var totalMillivolts float64
	var count int
	for _, cell := range cells {
		if cell == 0 || cell == math.MaxUint16 {
			continue
		}
		totalMillivolts += float64(cell)
		count++
	}
	if count == 0 {
		return 0
	}
	return totalMillivolts / 1000
}
