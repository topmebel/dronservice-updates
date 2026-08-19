package main

import (
	"strings"
	"testing"
)

func TestStarlinkPageShowsConnectionErrorLog(t *testing.T) {
	for _, fragment := range []string{
		`href="/starlink">Starlink`,
		`Журнал ошибок подключения`,
		`data-chart-range`,
		`historyRange`,
		`fetch('/api/starlink?range='`,
		`page-heading`,
		`<button id="reboot-starlink"`,
		`id="network-info"`,
		`id="metrics-chart"`,
		`legend-item`,
		`Линии на графике`,
		`загрузка (↓ Мбит/с)`,
		`app-shell`,
		`src="/assets/application-status.js" defer`,
		`<link rel="stylesheet" href="/assets/application.css">`,
	} {
		if !strings.Contains(starlinkPageHTML, fragment) {
			t.Errorf("Starlink page does not contain %q", fragment)
		}
	}
}
