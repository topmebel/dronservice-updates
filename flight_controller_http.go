package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"DronService/internal/mavlink"
)

type flightControllerConfigRequest struct {
	Enabled         bool   `json:"enabled"`
	Transport       string `json:"transport"`
	UDPAddr         string `json:"udpAddr"`
	OutSystemID     uint8  `json:"outSystemId"`
	TargetSystemID  uint8  `json:"targetSystemId"`
	LinkTimeout     string `json:"linkTimeout"`
	MessageInterval string `json:"messageInterval"`
}

func flightControllerConfigHandler(service *mavlink.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, service.Config())
		case http.MethodPut:
			var request flightControllerConfigRequest
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
				return
			}
			config := service.Config()
			config.Enabled = request.Enabled
			if request.Transport != "" {
				config.Transport = request.Transport
			}
			if request.UDPAddr != "" {
				config.UDPAddr = request.UDPAddr
			}
			if request.OutSystemID != 0 {
				config.OutSystemID = request.OutSystemID
			}
			config.TargetSystemID = request.TargetSystemID
			if request.LinkTimeout != "" {
				parsed, err := time.ParseDuration(request.LinkTimeout)
				if err != nil || parsed <= 0 {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid link timeout"})
					return
				}
				config.LinkTimeout = parsed
			}
			if request.MessageInterval != "" {
				parsed, err := time.ParseDuration(request.MessageInterval)
				if err != nil || parsed <= 0 {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message interval"})
					return
				}
				config.MessageInterval = parsed
			}
			if err := config.Validate(); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := service.UpdateConfig(config); err != nil {
				log.Printf("update flight controller config: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "flight controller configuration could not be saved"})
				return
			}
			writeJSON(w, http.StatusOK, service.Config())
		default:
			w.Header().Set("Allow", "GET, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func flightControllerStatusHandler(service *mavlink.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, service.Snapshot())
	}
}

func flightControllerPageHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(flightControllerPageHTML))
}

const flightControllerPageHTML = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <script src="/assets/application-status.js" defer></script>
  <link rel="stylesheet" href="/assets/application.css">
  <title>DronService — Автопилот</title>
  <style>
    .page-heading{display:flex;flex-wrap:wrap;align-items:center;gap:12px;margin-bottom:18px}.status-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:14px;margin:18px 0}.metric-card{padding:16px;border:1px solid #374151;border-radius:10px;background:#1f2937}.metric-card h3{margin:0 0 10px;font-size:.95rem;color:#93c5fd}.metric-card .value{font-size:1.15rem;font-weight:650}.metric-card .meta{margin-top:8px;color:#9ca3af;font-size:.82rem}.status-line{display:flex;align-items:center;gap:9px}.status-dot{width:12px;height:12px;border-radius:50%;background:#6b7280}.status-dot.online{background:#22c55e}.status-dot.offline{background:#ef4444}.config-card{margin-top:24px;padding:18px;border:1px solid #374151;border-radius:10px;background:#111827}.config-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px;margin-top:14px}.config-grid label{display:flex;flex-direction:column;gap:6px;color:#9ca3af;font-size:.82rem}.config-grid input[type=text],.config-grid input[type=number]{padding:10px 12px;border:1px solid #4b5563;border-radius:6px;background:#0f172a;color:#fff}.config-grid input[type=checkbox]{width:18px;height:18px}.toggle-row{display:flex;align-items:center;gap:10px;color:#e5e7eb;font-size:.95rem}.actions{display:flex;gap:10px;margin-top:16px}button{padding:10px 14px;border:1px solid #2563eb;border-radius:6px;background:#2563eb;color:#fff;cursor:pointer}button:disabled{opacity:.6;cursor:wait}.notice{margin-top:12px;color:#86efac;font-size:.9rem}.error-box,.empty{padding:18px;border:1px solid #374151;border-radius:8px;background:#1f2937;color:#cbd5e1}.error-box{border-color:#7f1d1d;color:#fecaca}.status-text{margin-top:16px;padding:14px;border-left:3px solid #f59e0b;background:#1f2937;color:#fde68a;border-radius:6px}
  </style>
</head>
<body>
<div class="app-shell">
<aside class="app-sidebar">
  <nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a href="/devices">Аналог. камеры</a><a href="/ip-cameras">IP-камеры</a><a href="/streams">MediaMTX</a><a href="/starlink">Starlink</a><a class="active" href="/flight-controller">Автопилот</a><a href="/zerotier">ZeroTier</a></nav>
  <div id="network-info"><div id="network-addresses">Сеть: получение адресов…</div><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div>
</aside>
<main class="app-main">
  <div class="page-heading"><h1>Автопилот · MAVLink</h1><span id="link-badge" class="metric-card" style="padding:8px 12px;margin:0">Линк: …</span></div>
  <div id="content"><p class="empty">Получение телеметрии…</p></div>
  <script>
    function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'],sl=s.starlink;if(sl?.internetViaStarlink){e.textContent='Starlink: ↓'+Number(sl.downlinkMbps||0).toFixed(2)+' ↑'+Number(sl.uplinkMbps||0).toFixed(2)+' Мбит/с · '+Number(sl.pingMs||s.pingMs||0).toFixed(1)+' мс';e.title='Подключение '+(sl.topology==='direct'?'напрямую к Starlink':'через роутер')+' · текущая скорость обмена'}else{e.textContent='Интернет: '+state[0]+(sl?.detected?' · Starlink найден':'')}e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}updateInternetStatus();
    const content=document.querySelector('#content'),linkBadge=document.querySelector('#link-badge');
    const escapeHTML=value=>String(value??'').replace(/[&<>'"]/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[char]));
    const fmtCoord=value=>Number.isFinite(value)?value.toFixed(6):'—';
    const fmtNum=(value,digits=1)=>Number.isFinite(value)?Number(value).toFixed(digits):'—';
    function renderStatus(data){
      const link=data.link||{},flight=data.flight||{},position=data.position||{},attitude=data.attitude||{},battery=data.battery||{},hud=data.hud||{};
      linkBadge.innerHTML='<span class="status-line"><i class="status-dot '+(link.connected?'online':'offline')+'"></i>'+(data.enabled?(link.connected?'Линк активен':'Линк потерян'):'MAVLink выключен')+'</span>';
      let html='<div class="status-grid">';
      html+='<div class="metric-card"><h3>Полёт</h3><div class="value">'+(flight.armed?'Вооружён':'Разоружён')+'</div><div class="meta">Режим: '+escapeHTML(flight.customMode??'—')+' · '+escapeHTML(flight.systemStatus||'—')+'</div><div class="meta">'+escapeHTML(flight.autopilot||'—')+' · '+escapeHTML(flight.vehicleType||'—')+'</div></div>';
      html+='<div class="metric-card"><h3>GPS</h3><div class="value">'+fmtCoord(position.latitude)+', '+fmtCoord(position.longitude)+'</div><div class="meta">Высота: '+fmtNum(position.altitudeRelative)+' м (отн.) · '+fmtNum(position.altitudeMsl)+' м MSL</div><div class="meta">Спутники: '+escapeHTML(position.satellites??'—')+' · '+escapeHTML(position.gpsFixTypeName||position.gpsFixType||'—')+'</div></div>';
      html+='<div class="metric-card"><h3>Ориентация</h3><div class="value">R '+fmtNum(attitude.rollDeg)+'° · P '+fmtNum(attitude.pitchDeg)+'°</div><div class="meta">Yaw '+fmtNum(attitude.yawDeg)+'° · курс '+fmtNum(position.heading)+'°</div></div>';
      html+='<div class="metric-card"><h3>Батарея</h3><div class="value">'+fmtNum(battery.voltage,2)+' В · '+fmtNum(battery.remaining,0)+'%</div><div class="meta">Ток: '+fmtNum(battery.current,1)+' А</div></div>';
      html+='<div class="metric-card"><h3>Скорость</h3><div class="value">'+fmtNum(hud.groundspeed,1)+' м/с</div><div class="meta">Воздух: '+fmtNum(hud.airspeed,1)+' м/с · V/S '+fmtNum(hud.climb,1)+' м/с · газ '+escapeHTML(hud.throttle??'—')+'%</div></div>';
      html+='<div class="metric-card"><h3>MAVLink</h3><div class="value">System '+escapeHTML(link.systemId||'—')+'</div><div class="meta">Component '+escapeHTML(link.componentId||'—')+' · UDP '+escapeHTML(data.config?.udpAddr||'—')+'</div><div class="meta">Обновлено: '+escapeHTML(data.updatedAt?new Date(data.updatedAt).toLocaleString('ru-RU'):'—')+'</div></div>';
      html+='</div>';
      if(data.statusText){html+='<div class="status-text">'+escapeHTML(data.statusText)+'</div>'}
      const cfg=data.config||{};
      html+='<section class="config-card"><h2>Настройки на этом дроне</h2><p class="meta" style="margin-top:6px">Сохраняются в <code>/var/lib/dronservice/mavlink.json</code>. FC должен слать MAVLink по UDP на указанный адрес.</p><form id="fc-config-form"><div class="config-grid"><label class="toggle-row"><input id="fc-enabled" type="checkbox"'+(cfg.enabled?' checked':'')+'> Включить MAVLink</label><label>UDP адрес<input id="fc-udp-addr" type="text" value="'+escapeHTML(cfg.udpAddr||'0.0.0.0:14550')+'" required></label><label>Target system ID<input id="fc-target-system" type="number" min="0" max="255" value="'+escapeHTML(cfg.targetSystemId??0)+'"></label><label>Out system ID<input id="fc-out-system" type="number" min="1" max="255" value="'+escapeHTML(cfg.outSystemId||255)+'"></label><label>Таймаут линка<input id="fc-link-timeout" type="text" value="'+escapeHTML(cfg.linkTimeout||'5s')+'"></label><label>Интервал сообщений<input id="fc-message-interval" type="text" value="'+escapeHTML(cfg.messageInterval||'500ms')+'"></label></div><div class="actions"><button type="submit">Сохранить</button></div><div id="fc-save-notice" class="notice" hidden></div></form></section>';
      content.innerHTML=html;
      document.querySelector('#fc-config-form').onsubmit=saveConfig;
    }
    async function load(){
      try{
        const response=await fetch('/api/flight-controller/status',{cache:'no-store'});
        const data=await response.json();
        if(!response.ok)throw new Error(data.error||'MAVLink недоступен');
        renderStatus(data);
      }catch(error){
        content.innerHTML='<p class="error-box">'+escapeHTML(error.message)+'</p>';
      }
    }
    async function saveConfig(event){
      event.preventDefault();
      const button=event.currentTarget.querySelector('button'),notice=document.querySelector('#fc-save-notice');
      button.disabled=true;notice.hidden=true;
      const payload={enabled:document.querySelector('#fc-enabled').checked,transport:'udp',udpAddr:document.querySelector('#fc-udp-addr').value.trim(),outSystemId:Number(document.querySelector('#fc-out-system').value),targetSystemId:Number(document.querySelector('#fc-target-system').value),linkTimeout:document.querySelector('#fc-link-timeout').value.trim(),messageInterval:document.querySelector('#fc-message-interval').value.trim()};
      try{
        const response=await fetch('/api/flight-controller/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
        const data=await response.json();
        if(!response.ok)throw new Error(data.error||'Не удалось сохранить настройки');
        notice.textContent='Настройки сохранены';
        notice.hidden=false;
        await load();
      }catch(error){
        alert(error.message);
      }finally{
        button.disabled=false;
      }
    }
    load();setInterval(load,2000);
  </script>
</main>
</div>
</body>
</html>`
