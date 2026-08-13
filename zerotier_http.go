package main

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"

	"DronService/internal/zerotier"
)

var zeroTierNetworkIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{16}$`)

func zeroTierStatusHandler(client *zerotier.Client, updater *zerotier.Updater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := client.Snapshot(r.Context())
		if err == nil {
			snapshot.Available = true
		}
		updater.Enrich(r.Context(), &snapshot)
		if err != nil && snapshot.Installed {
			log.Printf("read ZeroTier status: %v", err)
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func zeroTierUpdateHandler(updater *zerotier.Updater) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := updater.Request(); err != nil {
			log.Printf("request ZeroTier update: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ZeroTier update could not be started"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"updating": true})
	}
}

func zeroTierJoinHandler(client *zerotier.Client) http.HandlerFunc {
	type requestBody struct {
		NetworkID string `json:"networkId"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var request requestBody
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		request.NetworkID = strings.ToLower(strings.TrimSpace(request.NetworkID))
		if !zeroTierNetworkIDPattern.MatchString(request.NetworkID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Network ID must contain exactly 16 hexadecimal characters"})
			return
		}
		if err := client.Join(r.Context(), request.NetworkID); err != nil {
			log.Printf("join ZeroTier network %s: %v", request.NetworkID, err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not join ZeroTier network"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "joining", "networkId": request.NetworkID})
	}
}

func zeroTierLeaveHandler(client *zerotier.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		networkID := strings.ToLower(strings.TrimSpace(r.PathValue("networkID")))
		if !zeroTierNetworkIDPattern.MatchString(networkID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Network ID"})
			return
		}
		if err := client.Leave(r.Context(), networkID); err != nil {
			log.Printf("leave ZeroTier network %s: %v", networkID, err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not leave ZeroTier network"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func zeroTierPageHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(zeroTierPageHTML))
}

const zeroTierPageHTML = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <script src="/assets/application-status.js" defer></script>
  <link rel="stylesheet" href="/assets/application.css">
  <title>DronService — ZeroTier</title>
  <style>
    :root{color-scheme:dark;font-family:system-ui,sans-serif}body{max-width:1100px;margin:40px auto;padding:0 20px;background:#111827;color:#e5e7eb}.main-nav{display:flex;align-items:center;gap:8px;margin-bottom:32px;padding:10px;border:1px solid #374151;border-radius:12px;background:#1f2937;box-shadow:0 8px 24px rgb(0 0 0 / 20%)}.main-nav .brand{margin:0 auto 0 6px;color:#f9fafb;font-weight:700;letter-spacing:.02em}.main-nav a{padding:9px 13px;border-radius:8px;color:#cbd5e1;text-decoration:none;transition:background .15s,color .15s}.main-nav a:hover,.main-nav a:focus-visible{background:#374151;color:#fff;outline:none}.main-nav a.active{background:#2563eb;color:#fff}@media(max-width:800px){.main-nav{align-items:stretch;flex-direction:column}.main-nav .brand{margin:4px 8px 8px}}h1{margin-bottom:8px}.node-card{display:flex;flex-wrap:wrap;gap:28px;margin:22px 0;padding:18px;border:1px solid #374151;border-radius:10px;background:#1f2937}.node-item span{display:block;margin-bottom:4px;color:#9ca3af;font-size:.85rem}.status-line{display:flex;align-items:center;gap:9px}.status-dot{display:inline-block;width:12px;height:12px;border-radius:50%;background:#6b7280;box-shadow:0 0 0 3px rgb(255 255 255 / 8%)}.status-dot.online,.network-status.ok{background:#22c55e}.status-dot.offline,.network-status.error{background:#ef4444}.join-form{display:flex;gap:10px;margin:24px 0}.join-form input{flex:1;min-width:220px}button,input{box-sizing:border-box;padding:10px 13px;border:1px solid #4b5563;border-radius:6px;background:#111827;color:#fff}button{cursor:pointer;background:#2563eb;border-color:#2563eb}button:disabled{cursor:wait;opacity:.6}.leave{background:#991b1b;border-color:#b91c1c}table{width:100%;border-collapse:collapse;background:#1f2937}th,td{padding:12px;border-bottom:1px solid #374151;text-align:left;vertical-align:middle}th{color:#93c5fd}.network-status{display:inline-block;width:10px;height:10px;margin-right:8px;border-radius:50%;background:#f59e0b}.empty,.error-box{padding:20px;border:1px solid #374151;border-radius:8px;background:#1f2937}.error-box{border-color:#7f1d1d;color:#fecaca}@media(max-width:700px){.join-form{align-items:stretch;flex-direction:column}.table-wrap{overflow-x:auto}}
  </style>
</head>
<body>
  <nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a href="/devices">Аналог. камеры</a><a href="/ip-cameras">IP-камеры</a><a href="/streams">MediaMTX</a><a class="active" href="/zerotier">ZeroTier</a></nav>
  <div id="network-info" style="display:flex;align-items:center;gap:10px;margin:-22px 6px 28px;color:#9ca3af;font-size:.9rem"><span id="network-addresses">Сеть: получение адресов…</span><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div>
  <h1 id="service-title">ZeroTier</h1>
  <div id="content"><p class="empty">Получение состояния ZeroTier…</p></div>
  <script>
    function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'];e.textContent='Интернет: '+state[0];e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}updateInternetStatus();function updateNetworkInfo(){fetch('/api/system/network').then(r=>{if(!r.ok)throw new Error('network unavailable');return r.json()}).then(s=>{const parts=['LAN: '+(s.lan?.join(', ')||'—'),'Wi-Fi: '+(s.wifi?.join(', ')||'—')];if(s.localName)parts.push('Имя: '+s.localName);document.querySelector('#network-addresses').textContent=parts.join(' · ')}).catch(()=>document.querySelector('#network-addresses').textContent='Сеть: адреса недоступны')}updateNetworkInfo();const content=document.querySelector('#content');
    const escapeHTML=value=>String(value??'').replace(/[&<>'"]/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[char]));
    const addressList=network=>network.assignedAddresses?.length?network.assignedAddresses.map(escapeHTML).join('<br>'):'—';
    function render(data){const status=data.status||{};document.querySelector('#service-title').textContent='ZeroTier'+(status.version?' · v'+status.version:'');if(!data.installed){content.innerHTML='<div class="empty"><h2>ZeroTier не установлен</h2><p>После установки сервис будет запускаться автоматически.</p><button id="install-zerotier" '+(data.updating?'disabled':'')+'>'+(data.updating?'Установка…':'Установить ZeroTier')+'</button></div>';const button=document.querySelector('#install-zerotier');if(data.updating){setTimeout(load,1500)}else{button.onclick=()=>startZeroTierAction(button)}return}const networks=data.networks||[];let html=''+(data.updateAvailable?'<button id="update-zerotier">Обновить ZeroTier</button>':'')+'<div class="node-card"><div class="node-item"><span>Состояние узла</span><div class="status-line"><i class="status-dot '+(status.online?'online':'offline')+'"></i>'+(status.online?'Онлайн':'Офлайн')+'</div></div><div class="node-item"><span>Node ID</span><strong>'+escapeHTML(status.address)+'</strong></div><div class="node-item"><span>TCP fallback</span><strong>'+(status.tcpFallbackActive?'Активен':'Не используется')+'</strong></div></div><form class="join-form" id="join-form"><input id="network-id" maxlength="16" pattern="[0-9a-fA-F]{16}" placeholder="Network ID — 16 символов" required><button>Подключиться</button></form>';
      if(networks.length){html+='<div class="table-wrap"><table><thead><tr><th>Сеть</th><th>Network ID</th><th>Состояние</th><th>IP-адреса</th><th>Интерфейс</th><th></th></tr></thead><tbody>'+networks.map(network=>'<tr><td>'+(escapeHTML(network.name)||'Без имени')+'<br><small>'+escapeHTML(network.type)+'</small></td><td><code>'+escapeHTML(network.id)+'</code></td><td><span class="network-status '+(network.status==='OK'?'ok':'error')+'"></span>'+escapeHTML(network.status)+'</td><td>'+addressList(network)+'</td><td>'+(escapeHTML(network.portDeviceName)||'—')+'</td><td><button class="leave" data-network-id="'+escapeHTML(network.id)+'">Выйти</button></td></tr>').join('')+'</tbody></table></div>'}else{html+='<p class="empty">Узел не подключён ни к одной сети.</p>'}content.innerHTML=html;
      const update=document.querySelector('#update-zerotier');if(update)update.onclick=()=>startZeroTierAction(update);document.querySelector('#join-form').onsubmit=joinNetwork;document.querySelectorAll('.leave').forEach(button=>button.onclick=()=>leaveNetwork(button));}
    async function load(){try{const response=await fetch('/api/zerotier');const data=await response.json();if(!response.ok)throw new Error(data.error||'ZeroTier недоступен');render(data)}catch(error){content.innerHTML='<p class="error-box">'+escapeHTML(error.message)+'</p>'}}
    async function startZeroTierAction(button){button.disabled=true;button.textContent=button.id==='install-zerotier'?'Установка…':'Обновление…';const response=await fetch('/api/zerotier/update',{method:'POST'});if(!response.ok){const data=await response.json();alert(data.error||'Не удалось запустить процесс');button.disabled=false;return}setTimeout(load,1500)}
    async function joinNetwork(event){event.preventDefault();const button=event.currentTarget.querySelector('button');button.disabled=true;const response=await fetch('/api/zerotier/networks',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({networkId:document.querySelector('#network-id').value})});const data=await response.json();if(!response.ok){alert(data.error||'Не удалось подключиться');button.disabled=false;return}setTimeout(load,700)}
    async function leaveNetwork(button){const id=button.dataset.networkId;if(!confirm('Выйти из сети '+id+'?'))return;button.disabled=true;const response=await fetch('/api/zerotier/networks/'+encodeURIComponent(id),{method:'DELETE'});if(!response.ok){const data=await response.json();alert(data.error||'Не удалось выйти из сети');button.disabled=false;return}setTimeout(load,500)}
    load();setInterval(()=>{if(!content.contains(document.activeElement))load()},5000);
  </script>
</body>
</html>`
