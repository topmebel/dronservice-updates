package main

import (
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"

	"DronService/internal/ipcamera"
)

func ipCamerasHandler(service *ipcamera.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"cameras": service.List()})
		case http.MethodPost:
			var request ipcamera.SaveRequest
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&request) != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid camera configuration"})
				return
			}
			if err := service.SaveWithCameraUpdate(r.Context(), request); err != nil {
				if errors.Is(err, ipcamera.ErrDahuaInitializationRequired) {
					writeJSON(w, http.StatusConflict, map[string]string{"error": "Сначала авторизуйте новую камеру", "code": "initialization_required"})
					return
				}
				if errors.Is(err, ipcamera.ErrDahuaCredentials) {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Камера отклонила логин или пароль", "code": "credentials_required"})
					return
				}
				if errors.Is(err, ipcamera.ErrDahuaUnavailable) {
					log.Printf("update Dahua camera network settings: %v", err)
					writeJSON(w, http.StatusGatewayTimeout, map[string]string{
						"error": "Камера недоступна с Raspberry Pi. Проверьте подключение камеры и сетевой маршрут к её текущему IP-адресу.",
						"code":  "camera_unreachable",
					})
					return
				}
				log.Printf("save IP camera: %v", err)
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Не удалось изменить настройки камеры"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func ipCameraDiscoveryHandler(service *ipcamera.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cameras, err := service.Discover(r.Context())
		if err != nil {
			log.Printf("discover IP cameras: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "IP camera discovery failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cameras": cameras})
	}
}

func ipCameraStatusHandler(service *ipcamera.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cameras, err := service.CheckStatus(r.Context())
		if err != nil {
			log.Printf("check IP camera status: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "IP camera status check failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cameras": cameras})
	}
}

func ipCameraVideoStreamsHandler(service *ipcamera.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		camera, err := service.RefreshVideoStreams(r.Context(), r.PathValue("cameraID"))
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"camera": camera})
			return
		}
		switch {
		case errors.Is(err, ipcamera.ErrCameraNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Камера не найдена"})
		case errors.Is(err, ipcamera.ErrDahuaInitializationRequired):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Сначала авторизуйте новую камеру"})
		case errors.Is(err, ipcamera.ErrDahuaCredentials):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Укажите корректные логин и пароль камеры"})
		case errors.Is(err, ipcamera.ErrDahuaUnavailable):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "Камера недоступна с Raspberry Pi"})
		default:
			log.Printf("read Dahua video stream settings: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Камера не вернула параметры видеопотоков"})
		}
	}
}

func newIPCamerasPageHandler(service *ipcamera.Service) (http.Handler, error) {
	page, err := template.New("ip-cameras").Funcs(template.FuncMap{"json": func(value any) string {
		data, _ := json.Marshal(value)
		return string(data)
	}}).Parse(ipCamerasPageHTML)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cameras := service.List()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := page.Execute(w, cameras); err != nil {
			log.Printf("render IP cameras page: %v", err)
		}
	}), nil
}

const ipCamerasPageHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><script src="/assets/application-status.js" defer></script><link rel="stylesheet" href="/assets/application.css"><title>DronService — IP-камеры</title><style>
:root{color-scheme:dark;font-family:system-ui,sans-serif}body{max-width:1120px;margin:40px auto;padding:0 20px;background:#111827;color:#e5e7eb}.main-nav{display:flex;align-items:center;gap:8px;margin-bottom:32px;padding:10px;border:1px solid #374151;border-radius:12px;background:#1f2937;box-shadow:0 8px 24px rgb(0 0 0 / 20%)}.main-nav .brand{margin:0 auto 0 6px;color:#f9fafb;font-weight:700;letter-spacing:.02em}.main-nav a{padding:9px 13px;border-radius:8px;color:#cbd5e1;text-decoration:none;transition:background .15s,color .15s}.main-nav a:hover,.main-nav a:focus-visible{background:#374151;color:#fff;outline:none}.main-nav a.active{background:#2563eb;color:#fff}@media(max-width:700px){.main-nav{align-items:stretch;flex-direction:column}.main-nav .brand{margin:4px 8px 8px}}.page-heading{display:flex;align-items:center;justify-content:space-between;gap:20px}.page-heading h1{margin:0}table{width:100%;border-collapse:collapse;background:#1f2937;margin-top:20px}th,td{padding:12px;border-bottom:1px solid #374151;text-align:left}tbody tr{cursor:pointer}tbody tr:hover{background:#273449}.status-cell{text-align:center;vertical-align:middle}.status-dot{display:inline-block;width:24px;height:24px;border-radius:50%;vertical-align:middle;box-shadow:none}.status-dot.online{background:#22c55e}.status-dot.offline{background:#ef4444}button,input,select{padding:10px;border-radius:6px;border:1px solid #4b5563}input,select{box-sizing:border-box;width:100%;background:#111827;color:#fff}label{display:block;margin:14px 0 5px}dialog{width:min(560px,calc(100% - 40px));background:#1f2937;color:#fff;border:1px solid #4b5563;border-radius:10px}.camera-device-info{margin:-8px 0 18px;color:#9ca3af;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.camera-name-row,.network-fields{display:flex;align-items:flex-end;gap:18px}.camera-name-field,.network-field{flex:1}.stream-info{padding:10px 12px;border:1px solid #374151;border-radius:6px;background:#111827;color:#cbd5e1;line-height:1.55}.media-toggle-label{display:flex;align-items:flex-start;flex-direction:column;gap:5px;margin:0 0 5px;white-space:nowrap}.media-toggle-label input{appearance:none;position:relative;width:42px;height:24px;padding:0;border:0;border-radius:24px;background:#4b5563;cursor:pointer;transition:.2s}.media-toggle-label input:before{content:"";position:absolute;width:18px;height:18px;left:3px;top:3px;border-radius:50%;background:#fff;transition:.2s}.media-toggle-label input:checked{background:#2563eb}.media-toggle-label input:checked:before{transform:translateX(18px)}@media(max-width:600px){.page-heading{align-items:stretch;flex-direction:column;gap:12px}.page-heading button{align-self:flex-start}.camera-name-row,.network-fields{align-items:stretch;flex-direction:column;gap:8px}.media-toggle-label{margin-top:0}}.actions{text-align:right;margin-top:20px}.camera-link{display:inline-flex;align-items:center;justify-content:center;margin-left:6px;padding:4px 7px;border:1px solid #4b5563;border-radius:5px;color:#bfdbfe;text-decoration:none;font-size:12px;line-height:1.2}.camera-link:hover{background:#374151;color:#fff}.authorize-link{border-color:#f59e0b;color:#fde68a}.initialization-cell{white-space:nowrap}.initialization-complete{color:#86efac}.initialization-unknown{color:#9ca3af}.toggle-cell{vertical-align:middle;text-align:center}.toggle{position:relative;display:inline-block;margin:0;width:42px;height:24px;padding:0;border:0;border-radius:24px;background:#4b5563;cursor:pointer;vertical-align:middle;transition:.2s}.toggle:before{content:"";position:absolute;width:18px;height:18px;left:3px;top:3px;border-radius:50%;background:#fff;transition:.2s}.toggle[aria-checked="true"]{background:#2563eb}.toggle[aria-checked="true"]:before{transform:translateX(18px)}.toggle[aria-readonly="true"]{cursor:default}.notice{position:fixed;right:24px;bottom:24px;z-index:10;min-width:310px;padding:14px 18px;border:1px solid #3b82f6;border-radius:10px;background:#1e3a5f;color:#dbeafe;box-shadow:0 10px 30px rgb(0 0 0 / 35%);line-height:1.5}.notice[hidden]{display:none}.discovery-title{font-weight:700;margin-bottom:7px}.discovery-step{display:flex;justify-content:space-between;gap:22px}.step-running{color:#fde68a}.step-complete{color:#86efac}.step-error{color:#fca5a5}
</style></head><body><nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a href="/devices">Аналог. камеры</a><a class="active" href="/ip-cameras">IP-камеры</a><a href="/streams">MediaMTX</a><a href="/zerotier">ZeroTier</a></nav><div id="network-info" style="display:flex;align-items:center;gap:10px;margin:-22px 6px 28px;color:#9ca3af;font-size:.9rem"><span id="network-addresses">Сеть: получение адресов…</span><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div><div class="page-heading"><h1>Список IP-камер</h1><button id="refresh-cameras">Найти камеры</button></div><div id="refresh-notice" class="notice" hidden>Обновление списка камер…</div><table><thead><tr><th>Имя</th><th>IP-адрес</th><th>MAC</th><th>Производитель</th><th>Модель</th><th>Инициализация</th><th class="toggle-cell">MediaMTX</th><th class="status-cell">Состояние</th></tr></thead><tbody id="ip-cameras-body">{{range .}}<tr data-camera='{{. | json}}'><td>{{if .Name}}{{.Name}}{{else}}Не настроена{{end}}</td><td>{{.Address}}</td><td>{{.MAC}}</td><td>{{.Manufacturer}}</td><td>{{if .Model}}{{.Model}}{{else}}Не определена{{end}}</td><td class="initialization-cell">{{if eq .InitializationStatus "uninitialized"}}<a class="camera-link authorize-link" href="http://{{.Address}}{{if and .HTTPPort (ne .HTTPPort 80)}}:{{.HTTPPort}}{{end}}/" target="_blank" rel="noopener noreferrer">Авторизовать</a>{{else if eq .InitializationStatus "initialized"}}<a class="camera-link initialization-complete" href="http://{{.Address}}{{if and .HTTPPort (ne .HTTPPort 80)}}:{{.HTTPPort}}{{end}}/" target="_blank" rel="noopener noreferrer">Открыть ↗</a>{{else}}<a class="camera-link initialization-unknown" href="http://{{.Address}}{{if and .HTTPPort (ne .HTTPPort 80)}}:{{.HTTPPort}}{{end}}/" target="_blank" rel="noopener noreferrer" title="DHIP не сообщил состояние инициализации">Неизвестно ↗</a>{{end}}</td><td class="toggle-cell"><button type="button" class="toggle ip-use-toggle" role="switch" aria-checked="{{if .Use}}true{{else}}false{{end}}" aria-readonly="true" title="Состояние использования в MediaMTX"></button></td><td class="status-cell"><span class="status-dot {{if .Online}}online{{else}}offline{{end}}" title="{{if .Online}}Активна{{else}}Неактивна{{end}}" role="img" aria-label="{{if .Online}}Активна{{else}}Неактивна{{end}}"></span></td></tr>{{end}}</tbody></table>
<dialog id="camera-dialog"><h2>Настройки IP-камеры</h2><p id="camera-device-info" class="camera-device-info"></p><div class="camera-name-row"><div class="camera-name-field"><label>Имя</label><input id="name" maxlength="100"></div><label class="media-toggle-label"><span>Использовать в MediaMTX</span><input id="use-camera" type="checkbox"></label></div><label>IP-адрес</label><input id="address"><div class="network-fields"><div class="network-field"><label>Маска подсети</label><input id="subnet-mask" inputmode="decimal"></div><div class="network-field"><label>Шлюз</label><input id="gateway" inputmode="decimal"></div></div><label>Логин</label><input id="username" autocomplete="off"><label>Пароль</label><input id="password" type="text" autocomplete="off"><p id="video-stream-info" class="stream-info">Параметры потоков ещё не получены</p><label>Main stream RTSP path</label><input id="main-rtsp"><label>Sub stream RTSP path</label><input id="sub-rtsp"><div class="actions"><button id="close">Закрыть</button> <button id="save">Сохранить</button></div></dialog><dialog id="credentials-dialog"><h2>Авторизация Dahua</h2><p>Сохранённые учётные данные не подошли. Укажите корректные данные камеры.</p><label>Логин</label><input id="retry-username" autocomplete="username"><label>Пароль</label><input id="retry-password" type="password" autocomplete="current-password"><div class="actions"><button id="cancel-credentials" type="button">Отмена</button> <button id="retry-save" type="button">Повторить</button></div></dialog><script>
function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'];e.textContent='Интернет: '+state[0];e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}updateInternetStatus();function updateNetworkInfo(){fetch('/api/system/network').then(r=>{if(!r.ok)throw new Error('network unavailable');return r.json()}).then(s=>{const parts=['LAN: '+(s.lan?.join(', ')||'—'),'Wi-Fi: '+(s.wifi?.join(', ')||'—')];if(s.localName)parts.push('Имя: '+s.localName);document.querySelector('#network-addresses').textContent=parts.join(' · ')}).catch(()=>document.querySelector('#network-addresses').textContent='Сеть: адреса недоступны')}updateNetworkInfo();const dialog=document.querySelector('#camera-dialog'),credentialsDialog=document.querySelector('#credentials-dialog'),deviceInfo=document.querySelector('#camera-device-info'),videoStreamInfo=document.querySelector('#video-stream-info'),usernameInput=document.querySelector('#username'),passwordInput=document.querySelector('#password'),mainRTSPInput=document.querySelector('#main-rtsp'),subRTSPInput=document.querySelector('#sub-rtsp');let camera;function rtspURLWithCredentials(value){try{const parsed=new URL(value);parsed.username=usernameInput.value;parsed.password=passwordInput.value;return parsed.toString()}catch(error){return value}}function refreshRTSPCredentials(){mainRTSPInput.value=rtspURLWithCredentials(mainRTSPInput.value);subRTSPInput.value=rtspURLWithCredentials(subRTSPInput.value)}function streamDescription(label,stream){return label+': '+(stream?.resolution||'—')+' · '+(stream?.fps?stream.fps+' FPS':'—')}function renderVideoStreams(value){videoStreamInfo.textContent=streamDescription('Основной поток',value.mainStream)+'; '+streamDescription('Дополнительный поток',value.subStream)}async function refreshVideoStreams(value){if((value.manufacturer||'')!=='Dahua')return;const id=value.id;videoStreamInfo.textContent='Получение параметров потоков…';try{const response=await fetch('/api/ip-cameras/'+encodeURIComponent(id)+'/video-streams',{method:'POST'}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось получить параметры потоков');if(camera?.id===id){camera=result.camera;renderVideoStreams(camera)}}catch(error){if(camera?.id===id)videoStreamInfo.textContent=error.message}}function openCamera(value){if(value.initializationStatus==='uninitialized'){alert('Сначала нажмите «Авторизовать» и завершите первичную инициализацию камеры.');return}camera=value;const manufacturer=camera.manufacturer||'Производитель не определён',model=camera.model||'модель не определена';deviceInfo.textContent=manufacturer+' · '+model;document.querySelector('#name').value=camera.name||'';document.querySelector('#use-camera').checked=!!camera.use;document.querySelector('#address').value=camera.address||'';document.querySelector('#subnet-mask').value=camera.subnetMask||'';document.querySelector('#gateway').value=camera.gateway||'';usernameInput.value=camera.username||'';passwordInput.value=camera.password||'';mainRTSPInput.value=camera.mainStreamPath||'';subRTSPInput.value=camera.subStreamPath||'';refreshRTSPCredentials();renderVideoStreams(camera);dialog.showModal();refreshVideoStreams(camera)}for(const input of [usernameInput,passwordInput])input.addEventListener('input',refreshRTSPCredentials);for(const input of [mainRTSPInput,subRTSPInput])input.addEventListener('change',refreshRTSPCredentials);const ipBody=document.querySelector('#ip-cameras-body');ipBody.ondblclick=event=>{if(event.target.closest('.toggle,.camera-link'))return;const row=event.target.closest('tr[data-camera]');if(row)openCamera(JSON.parse(row.dataset.camera))};document.querySelector('#close').onclick=()=>dialog.close();let pendingPayload;function cameraPayload(){refreshRTSPCredentials();return{id:camera.id,name:document.querySelector('#name').value,use:document.querySelector('#use-camera').checked,address:document.querySelector('#address').value,subnetMask:document.querySelector('#subnet-mask').value,gateway:document.querySelector('#gateway').value,manufacturer:camera.manufacturer,model:camera.model,username:usernameInput.value,password:passwordInput.value,mainStreamPath:mainRTSPInput.value,subStreamPath:subRTSPInput.value}}async function submitCamera(payload){const response=await fetch('/api/ip-cameras',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});const result=await response.json().catch(()=>({}));if(response.status===401&&result.code==='credentials_required'){pendingPayload=payload;document.querySelector('#retry-username').value=payload.username||camera.username||'admin';document.querySelector('#retry-password').value='';credentialsDialog.showModal();return}if(!response.ok){alert(result.error||'Не удалось сохранить камеру');return}location.reload()}document.querySelector('#save').onclick=()=>submitCamera(cameraPayload());document.querySelector('#cancel-credentials').onclick=()=>credentialsDialog.close();document.querySelector('#retry-save').onclick=()=>{pendingPayload.username=document.querySelector('#retry-username').value;pendingPayload.password=document.querySelector('#retry-password').value;credentialsDialog.close();submitCamera(pendingPayload)};
async function refreshIPCameras(){if(dialog.open||!document.hidden&&document.activeElement?.matches('input,select'))return;try{const html=await fetch('/ip-cameras',{cache:'no-store'}).then(r=>r.text());const next=new DOMParser().parseFromString(html,'text/html').querySelector('#ip-cameras-body');if(next)document.querySelector('#ip-cameras-body').replaceChildren(...next.children)}catch(error){}}setInterval(refreshIPCameras,5000);const discoveryMechanisms=['Dahua — DHIP','UNV — XML probe UDP/3702 → 3705','UNV — ARP fallback'];function renderDiscoveryState(state){const labels={running:'Выполняется…',complete:'Завершено',error:'Ошибка'};const notice=document.querySelector('#refresh-notice');notice.innerHTML='<div class="discovery-title">Поиск новых камер</div>'+discoveryMechanisms.map(name=>'<div class="discovery-step"><span>'+name+'</span><span class="step-'+state+'">'+labels[state]+'</span></div>').join('');notice.hidden=false}async function discoverCameras(){const button=document.querySelector('#refresh-cameras');button.disabled=true;renderDiscoveryState('running');try{const response=await fetch('/api/ip-cameras/discover',{method:'POST'});if(!response.ok)throw new Error('Не удалось обновить список камер');renderDiscoveryState('complete');await new Promise(resolve=>setTimeout(resolve,900));location.replace('/ip-cameras?refreshed=1')}catch(error){renderDiscoveryState('error');button.disabled=false;setTimeout(()=>document.querySelector('#refresh-notice').hidden=true,4000)}}async function checkCameraStatus(){const notice=document.querySelector('#refresh-notice');notice.textContent='Проверка состояния камер…';notice.hidden=false;try{const response=await fetch('/api/ip-cameras/status',{method:'POST'});if(!response.ok)throw new Error('Не удалось проверить состояние камер');location.replace('/ip-cameras?refreshed=1')}catch(error){notice.textContent=error.message;setTimeout(()=>notice.hidden=true,4000)}}document.querySelector('#refresh-cameras').onclick=discoverCameras;if(new URLSearchParams(location.search).get('refreshed')!=='1')checkCameraStatus();
</script></body></html>`
