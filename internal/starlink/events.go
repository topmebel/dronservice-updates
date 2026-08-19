package starlink

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b0ch3nski/go-starlink/starlink/model/device"
)

const maxStarlinkEvents = 80

type Event struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Count   int       `json:"count,omitempty"`
}

func (s *Service) loadEvents() {
	if s.eventsPath == "" {
		return
	}
	data, err := os.ReadFile(s.eventsPath)
	if err != nil {
		return
	}
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return
	}
	s.events = events
}

func (s *Service) saveEventsLocked() {
	if s.eventsPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.eventsPath), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s.events)
	if err != nil {
		return
	}
	_ = os.WriteFile(s.eventsPath, data, 0o600)
}

func (s *Service) recordEvent(level, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	if n := len(s.events); n > 0 {
		last := &s.events[n-1]
		if last.Level == level && last.Message == message && now.Sub(last.Time) < 15*time.Minute {
			last.Count++
			if last.Count < 1 {
				last.Count = 1
			}
			last.Time = now
			s.saveEventsLocked()
			return
		}
	}
	s.events = append(s.events, Event{Time: now, Level: level, Message: message, Count: 1})
	if len(s.events) > maxStarlinkEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-maxStarlinkEvents:]...)
	}
	s.saveEventsLocked()
}

func copyEventsNewestFirst(events []Event) []Event {
	result := make([]Event, len(events))
	for index, event := range events {
		result[len(events)-1-index] = event
	}
	return result
}

func dishAlerts(dish *device.DishGetStatusResponse) []string {
	if dish == nil {
		return nil
	}
	alerts := make([]string, 0, 8)
	if stats := dish.GetObstructionStats(); stats != nil && (stats.GetCurrentlyObstructed() || stats.GetFractionObstructed() >= 0.01) {
		alerts = append(alerts, "Препятствия в зоне обзора")
	}
	source := dish.GetAlerts()
	if source == nil {
		return alerts
	}
	for _, candidate := range []struct {
		active bool
		text   string
	}{
		{source.GetNoEthernetLink(), "Нет Ethernet-линка"},
		{source.GetMotorsStuck(), "Моторы антенны заклинило"},
		{source.GetThermalShutdown(), "Термовыключение терминала"},
		{source.GetThermalThrottle(), "Термоограничение терминала"},
		{source.GetPowerSupplyThermalThrottle(), "Термоограничение блока питания"},
		{source.GetMastNotNearVertical(), "Мачта отклонена от вертикали"},
		{source.GetUnexpectedLocation(), "Неожиданное местоположение"},
		{source.GetSlowEthernetSpeeds() || source.GetSlowEthernetSpeeds_100(), "Медленный Ethernet"},
		{source.GetUpsuRouterPortSlow(), "Медленный порт роутера"},
		{source.GetDishWaterDetected(), "Обнаружена вода на антенне"},
		{source.GetRouterWaterDetected(), "Обнаружена вода на роутере"},
		{source.GetLowMotorCurrent(), "Низкий ток моторов"},
		{source.GetLowerSignalThanPredicted(), "Сигнал ниже ожидаемого"},
		{source.GetRoaming(), "Режим роуминга"},
		{source.GetInstallPending(), "Ожидается установка ПО"},
	} {
		if candidate.active {
			alerts = append(alerts, candidate.text)
		}
	}
	return alerts
}

func outageMessage(cause device.DishOutage_Cause) string {
	switch cause {
	case device.DishOutage_BOOTING:
		return "Терминал загружается"
	case device.DishOutage_STOWED:
		return "Антенна сложена"
	case device.DishOutage_THERMAL_SHUTDOWN:
		return "Термовыключение"
	case device.DishOutage_NO_SCHEDULE:
		return "Нет расписания спутников"
	case device.DishOutage_NO_SATS:
		return "Нет спутников в зоне видимости"
	case device.DishOutage_OBSTRUCTED:
		return "Связь прервана из-за препятствий"
	case device.DishOutage_NO_DOWNLINK:
		return "Нет нисходящего канала"
	case device.DishOutage_NO_PINGS:
		return "Нет ответа наземной станции"
	case device.DishOutage_ACTUATOR_ACTIVITY:
		return "Антенна позиционируется"
	case device.DishOutage_CABLE_TEST:
		return "Выполняется тест кабеля"
	case device.DishOutage_SLEEPING:
		return "Терминал в спящем режиме"
	case device.DishOutage_SKY_SEARCH:
		return "Поиск открытого неба"
	case device.DishOutage_INHIBIT_RF:
		return "Радиоканал отключён"
	default:
		return "Сбой подключения Starlink"
	}
}

func outageLevel(cause device.DishOutage_Cause) string {
	switch cause {
	case device.DishOutage_BOOTING, device.DishOutage_ACTUATOR_ACTIVITY, device.DishOutage_SKY_SEARCH, device.DishOutage_SLEEPING, device.DishOutage_CABLE_TEST:
		return "warning"
	default:
		return "error"
	}
}

func newAlertEvents(previous, current []string) []string {
	seen := make(map[string]struct{}, len(previous))
	for _, alert := range previous {
		seen[alert] = struct{}{}
	}
	added := make([]string, 0)
	for _, alert := range current {
		if _, exists := seen[alert]; !exists {
			added = append(added, alert)
		}
	}
	return added
}
