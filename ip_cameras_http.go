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
:root{color-scheme:dark;font-family:system-ui,sans-serif}body{max-width:1120px;margin:40px auto;padding:0 20px;background:#111827;color:#e5e7eb}.main-nav{display:flex;align-items:center;gap:8px;margin-bottom:32px;padding:10px;border:1px solid #374151;border-radius:12px;background:#1f2937;box-shadow:0 8px 24px rgb(0 0 0 / 20%)}.main-nav .brand{margin:0 auto 0 6px;color:#f9fafb;font-weight:700;letter-spacing:.02em}.main-nav a{padding:9px 13px;border-radius:8px;color:#cbd5e1;text-decoration:none;transition:background .15s,color .15s}.main-nav a:hover,.main-nav a:focus-visible{background:#374151;color:#fff;outline:none}.main-nav a.active{background:#2563eb;color:#fff}@media(max-width:700px){.main-nav{align-items:stretch;flex-direction:column}.main-nav .brand{margin:4px 8px 8px}}.page-heading{display:flex;align-items:center;justify-content:space-between;gap:20px}.page-heading h1{margin:0}table{width:100%;border-collapse:collapse;background:#1f2937;margin-top:20px}th,td{padding:12px;border-bottom:1px solid #374151;text-align:left}tbody tr{cursor:pointer}tbody tr:hover{background:#273449}.status-cell{text-align:center;vertical-align:middle}.status-dot{display:inline-block;width:24px;height:24px;border-radius:50%;vertical-align:middle;box-shadow:none}.status-dot.online{background:#22c55e}.status-dot.offline{background:#ef4444}button,input,select{padding:10px;border-radius:6px;border:1px solid #4b5563}input,select{box-sizing:border-box;width:100%;background:#111827;color:#fff}label{display:block;margin:14px 0 5px}dialog{width:min(560px,calc(100% - 40px));background:#1f2937;color:#fff;border:1px solid #4b5563;border-radius:10px}.camera-device-info{margin:-8px 0 18px;color:#9ca3af;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.camera-name-row,.network-fields,.credentials-fields{display:flex;align-items:flex-end;gap:18px}.camera-name-field,.network-field,.credential-field{flex:1}.media-toggle-label{display:flex;align-items:flex-start;flex-direction:column;gap:5px;margin:0 0 5px;white-space:nowrap}.media-toggle-label input{appearance:none;position:relative;width:42px;height:24px;padding:0;border:0;border-radius:24px;background:#4b5563;cursor:pointer;transition:.2s}.media-toggle-label input:before{content:"";position:absolute;width:18px;height:18px;left:3px;top:3px;border-radius:50%;background:#fff;transition:.2s}.media-toggle-label input:checked{background:#2563eb}.media-toggle-label input:checked:before{transform:translateX(18px)}@media(max-width:600px){.page-heading{align-items:stretch;flex-direction:column;gap:12px}.page-heading button{align-self:flex-start}.camera-name-row,.network-fields,.credentials-fields{align-items:stretch;flex-direction:column;gap:8px}.media-toggle-label{margin-top:0}}.actions{text-align:right;margin-top:20px}.camera-link{display:inline-flex;align-items:center;justify-content:center;margin-left:6px;padding:4px 7px;border:1px solid #4b5563;border-radius:5px;color:#bfdbfe;text-decoration:none;font-size:12px;line-height:1.2}.camera-link:hover{background:#374151;color:#fff}.authorize-link{border-color:#f59e0b;color:#fde68a}.initialization-cell{white-space:nowrap}.initialization-complete{color:#86efac}.initialization-unknown{color:#9ca3af}.toggle-cell{vertical-align:middle;text-align:center}.toggle{position:relative;display:inline-block;margin:0;width:42px;height:24px;padding:0;border:0;border-radius:24px;background:#4b5563;cursor:pointer;vertical-align:middle;transition:.2s}.toggle:before{content:"";position:absolute;width:18px;height:18px;left:3px;top:3px;border-radius:50%;background:#fff;transition:.2s}.toggle[aria-checked="true"]{background:#2563eb}.toggle[aria-checked="true"]:before{transform:translateX(18px)}.toggle[aria-readonly="true"]{cursor:default}.notice{position:fixed;right:24px;bottom:24px;z-index:10;min-width:310px;padding:14px 18px;border:1px solid #3b82f6;border-radius:10px;background:#1e3a5f;color:#dbeafe;box-shadow:0 10px 30px rgb(0 0 0 / 35%);line-height:1.5}.notice[hidden]{display:none}.discovery-title{font-weight:700;margin-bottom:7px}.discovery-step{display:flex;justify-content:space-between;gap:22px}.step-running{color:#fde68a}.step-complete{color:#86efac}.step-error{color:#fca5a5}.table-wrap{overflow-x:auto;margin-top:20px;border-radius:10px}.table-wrap table{min-width:1020px;margin-top:0}.camera-row td{border-bottom:0!important}.stream-details-row{cursor:default;background:#172033}.stream-details-row:hover{background:#172033!important}.stream-details-row td{padding:8px 12px 12px!important;border-bottom:1px solid #4b5563!important}.stream-details{display:flex;align-items:center;flex-wrap:wrap;gap:8px 28px}.stream-detail{white-space:nowrap;font-size:.82rem;color:#cbd5e1}.stream-detail strong{margin-right:5px;color:#93c5fd}.preview-cell{text-align:center;white-space:nowrap}.preview-button{min-height:32px;padding:5px 9px}.preview-dialog{width:min(960px,calc(100% - 40px))}.preview-header{display:flex;align-items:center;justify-content:space-between;gap:16px}.preview-header h2{margin:0}.preview-metadata{min-height:1.5em;margin:12px 0 0;padding:9px 12px;border:1px solid #374151;border-radius:6px;background:#111827;color:#cbd5e1}.preview-frame{display:block;width:100%;aspect-ratio:16/9;margin-top:12px;border:0;border-radius:8px;background:#000}.preview-status{min-height:1.5em;margin:10px 0 0;color:#cbd5e1}
</style></head><body><nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a href="/devices">Аналог. камеры</a><a class="active" href="/ip-cameras">IP-камеры</a><a href="/streams">MediaMTX</a><a href="/zerotier">ZeroTier</a></nav><div id="network-info" style="display:flex;align-items:center;gap:10px;margin:-22px 6px 28px;color:#9ca3af;font-size:.9rem"><span id="network-addresses">Сеть: получение адресов…</span><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div><div class="page-heading"><h1>Список IP-камер</h1><button id="refresh-cameras">Найти камеры</button></div><div id="refresh-notice" class="notice" hidden>Обновление списка камер…</div><div class="table-wrap"><table><thead><tr><th>Имя</th><th>IP-адрес</th><th>Производитель</th><th>Модель</th><th>Инициализация</th><th class="toggle-cell">MediaMTX</th><th class="status-cell">Состояние</th></tr></thead><tbody id="ip-cameras-body">{{range .}}<tr class="camera-row" data-camera='{{. | json}}'><td>{{if .Name}}{{.Name}}{{else}}Не настроена{{end}}</td><td>{{.Address}}</td><td>{{.Manufacturer}}</td><td>{{if .Model}}{{.Model}}{{else}}Не определена{{end}}</td><td class="initialization-cell"><button type="button" class="camera-link {{if eq .InitializationStatus "uninitialized"}}authorize-link{{else if eq .InitializationStatus "initialized"}}initialization-complete{{else}}initialization-unknown{{end}}" data-camera-access="{{.ID}}">{{if eq .InitializationStatus "uninitialized"}}Авторизовать{{else if eq .InitializationStatus "initialized"}}Инициализирована · Открыть ↗{{else}}Неизвестно · Открыть ↗{{end}}</button></td><td class="toggle-cell"><button type="button" class="toggle ip-use-toggle" role="switch" aria-checked="{{if .Use}}true{{else}}false{{end}}" aria-readonly="true" title="Состояние использования в MediaMTX"></button></td><td class="status-cell"><span class="status-dot {{if .Online}}online{{else}}offline{{end}}" title="{{if .Online}}Активна{{else}}Неактивна{{end}}" role="img" aria-label="{{if .Online}}Активна{{else}}Неактивна{{end}}"></span></td></tr><tr class="stream-details-row" data-stream-details="{{.ID}}"><td colspan="7"><div class="stream-details"><span class="stream-detail"><strong>Main:</strong><span data-main-stream>{{if .MainStream.Resolution}}{{.MainStream.Resolution}}{{else}}—{{end}} · {{if .MainStream.FPS}}{{.MainStream.FPS}} FPS{{else}}—{{end}} · {{if .MainStream.BitrateKbps}}{{.MainStream.BitrateKbps}} кбит/с{{else}}—{{end}}</span><button type="button" class="camera-link preview-button" data-camera-preview="{{.ID}}" data-preview-stream="main" aria-label="Открыть Main stream камеры {{if .Name}}{{.Name}}{{else}}{{.Address}}{{end}}">Просмотр ▶</button></span><span class="stream-detail"><strong>Sub:</strong><span data-sub-stream>{{if .SubStream.Resolution}}{{.SubStream.Resolution}}{{else}}—{{end}} · {{if .SubStream.FPS}}{{.SubStream.FPS}} FPS{{else}}—{{end}} · {{if .SubStream.BitrateKbps}}{{.SubStream.BitrateKbps}} кбит/с{{else}}—{{end}}</span><button type="button" class="camera-link preview-button" data-camera-preview="{{.ID}}" data-preview-stream="sub" aria-label="Открыть Sub stream камеры {{if .Name}}{{.Name}}{{else}}{{.Address}}{{end}}">Просмотр ▶</button></span></div></td></tr>{{end}}</tbody></table></div>
<dialog id="camera-dialog"><h2>Настройки IP-камеры</h2><p id="camera-device-info" class="camera-device-info"></p><div class="camera-name-row"><div class="camera-name-field"><label>Имя</label><input id="name" maxlength="100"></div><label class="media-toggle-label"><span>Использовать в MediaMTX</span><input id="use-camera" type="checkbox"></label></div><label>IP-адрес</label><input id="address"><div class="network-fields"><div class="network-field"><label>Маска подсети</label><input id="subnet-mask" inputmode="decimal"></div><div class="network-field"><label>Шлюз</label><input id="gateway" inputmode="decimal"></div></div><div class="credentials-fields"><div class="credential-field"><label for="username">Логин</label><input id="username" autocomplete="off"></div><div class="credential-field"><label for="password">Пароль</label><input id="password" type="password" autocomplete="new-password"></div></div><label>Main stream RTSP path</label><input id="main-rtsp"><label>Sub stream RTSP path</label><input id="sub-rtsp"><div class="actions"><button id="close">Закрыть</button> <button id="save">Сохранить</button></div></dialog><dialog id="credentials-dialog"><h2>Авторизация Dahua</h2><p>Сохранённые учётные данные не подошли. Укажите корректные данные камеры.</p><label>Логин</label><input id="retry-username" autocomplete="username"><label>Пароль</label><input id="retry-password" type="password" autocomplete="current-password"><div class="actions"><button id="cancel-credentials" type="button">Отмена</button> <button id="retry-save" type="button">Повторить</button></div></dialog><dialog id="camera-preview-dialog" class="preview-dialog" aria-labelledby="camera-preview-title" aria-describedby="camera-preview-metadata"><div class="preview-header"><h2 id="camera-preview-title">Предпросмотр</h2><button id="camera-preview-close" type="button">Закрыть</button></div><p id="camera-preview-metadata" class="preview-metadata" aria-live="polite"></p><iframe id="camera-preview-frame" class="preview-frame" title="Предпросмотр IP-камеры" scrolling="no" sandbox="allow-scripts allow-same-origin" allow="autoplay; fullscreen" referrerpolicy="no-referrer"></iframe><p id="camera-preview-status" class="preview-status" role="status" aria-live="polite"></p></dialog><script>
function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'];e.textContent='Интернет: '+state[0];e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}
updateInternetStatus();
function updateNetworkInfo(){fetch('/api/system/network').then(r=>{if(!r.ok)throw new Error('network unavailable');return r.json()}).then(s=>{const parts=['LAN: '+(s.lan?.join(', ')||'—'),'Wi-Fi: '+(s.wifi?.join(', ')||'—')];if(s.localName)parts.push('Имя: '+s.localName);document.querySelector('#network-addresses').textContent=parts.join(' · ')}).catch(()=>document.querySelector('#network-addresses').textContent='Сеть: адреса недоступны')}
updateNetworkInfo();
const dialog=document.querySelector('#camera-dialog'),credentialsDialog=document.querySelector('#credentials-dialog'),deviceInfo=document.querySelector('#camera-device-info'),addressInput=document.querySelector('#address'),usernameInput=document.querySelector('#username'),passwordInput=document.querySelector('#password'),mainRTSPInput=document.querySelector('#main-rtsp'),subRTSPInput=document.querySelector('#sub-rtsp'),previewDialog=document.querySelector('#camera-preview-dialog'),previewFrame=document.querySelector('#camera-preview-frame'),previewTitle=document.querySelector('#camera-preview-title'),previewMetadata=document.querySelector('#camera-preview-metadata'),previewStatus=document.querySelector('#camera-preview-status');
let camera,previewSession=null;
const videoStreamRequests=new Map(),videoStreamsLoaded=new Set();
function rtspURLFromInputs(value){try{const parsed=new URL(value),address=addressInput.value.trim();if(address)parsed.hostname=address;parsed.username='';parsed.password='';return parsed.toString()}catch(error){return value}}
function refreshRTSPPaths(){mainRTSPInput.value=rtspURLFromInputs(mainRTSPInput.value);subRTSPInput.value=rtspURLFromInputs(subRTSPInput.value)}
function streamMetadata(stream){return[(stream?.resolution||'—'),(stream?.fps?stream.fps+' FPS':'—'),(stream?.bitrateKbps?stream.bitrateKbps+' кбит/с':'—')].join(' · ')}
function previewStreamLabel(kind){return kind==='sub'?'Sub':'Main'}
function renderCameraPreviewFallback(details,kind){const current=details?.querySelector(kind==='sub'?'[data-sub-stream]':'[data-main-stream]')?.textContent?.trim()||'— · — · —';previewMetadata.textContent='Поток: '+previewStreamLabel(kind)+' · '+current}
function renderCameraPreviewMetadata(value,fallbackKind){const kind=value?.kind==='sub'?'sub':value?.kind==='main'?'main':fallbackKind;previewMetadata.textContent='Поток: '+previewStreamLabel(kind)+' · Разрешение: '+(value?.resolution||'—')+' · FPS: '+(value?.fps||'—')+' · Битрейт: '+(value?.bitrateKbps?value.bitrateKbps+' кбит/с':'—')}
function isDahuaCamera(value){return(value.manufacturer||'')==='Dahua'||(value.protocol||'').split(',').some(protocol=>protocol.trim()==='DHIP')}
function requestVideoStreams(value){if(!isDahuaCamera(value)||value.initializationStatus==='uninitialized')return Promise.resolve(value);if(videoStreamRequests.has(value.id))return videoStreamRequests.get(value.id);const request=(async()=>{try{const response=await fetch('/api/ip-cameras/'+encodeURIComponent(value.id)+'/video-streams',{method:'POST'}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось получить параметры потоков');videoStreamsLoaded.add(value.id);return result.camera}finally{videoStreamRequests.delete(value.id)}})();videoStreamRequests.set(value.id,request);return request}
function cameraStreamDetailsRow(row,id){const details=row.nextElementSibling;return details?.dataset.streamDetails===id?details:null}
function updateCameraRow(row,value){const details=cameraStreamDetailsRow(row,value.id);if(details){details.querySelector('[data-main-stream]').textContent=streamMetadata(value.mainStream);details.querySelector('[data-sub-stream]').textContent=streamMetadata(value.subStream)}row.dataset.camera=JSON.stringify(value);if(value.initializationStatus==='initialized'){const access=row.querySelector('[data-camera-access]');access.classList.remove('authorize-link','initialization-unknown');access.classList.add('initialization-complete');access.textContent='Инициализирована · Открыть ↗'}}
function loadTableVideoStreams(){for(const row of ipBody.querySelectorAll('tr[data-camera]')){let value;try{value=JSON.parse(row.dataset.camera)}catch(error){continue}if(!isDahuaCamera(value)||!value.hasPassword||value.initializationStatus==='uninitialized'||videoStreamsLoaded.has(value.id))continue;requestVideoStreams(value).then(refreshed=>{if(row.isConnected)updateCameraRow(row,refreshed)}).catch(error=>{const details=cameraStreamDetailsRow(row,value.id);if(details)for(const cell of details.querySelectorAll('[data-main-stream],[data-sub-stream]'))cell.title=error.message})}}
function openCamera(value){if(value.initializationStatus==='uninitialized'){alert('Сначала нажмите «Авторизовать» и завершите первичную инициализацию камеры.');return}camera=value;const manufacturer=camera.manufacturer||'Производитель не определён',model=camera.model||'модель не определена';deviceInfo.textContent=manufacturer+' · '+model;document.querySelector('#name').value=camera.name||'';document.querySelector('#use-camera').checked=!!camera.use;addressInput.value=camera.address||'';document.querySelector('#subnet-mask').value=camera.subnetMask||'';document.querySelector('#gateway').value=camera.gateway||'';usernameInput.value=camera.username||'';passwordInput.value='';passwordInput.placeholder=camera.hasPassword?'Пароль сохранён — оставьте пустым, чтобы не менять':'Введите пароль';mainRTSPInput.value=camera.mainStreamPath||'';subRTSPInput.value=camera.subStreamPath||'';refreshRTSPPaths();dialog.showModal()}
addressInput.addEventListener('input',refreshRTSPPaths);
for(const input of [mainRTSPInput,subRTSPInput])input.addEventListener('change',refreshRTSPPaths);
const ipBody=document.querySelector('#ip-cameras-body');
ipBody.ondblclick=event=>{if(event.target.closest('.toggle,.camera-link'))return;const row=event.target.closest('tr[data-camera]');if(row)openCamera(JSON.parse(row.dataset.camera))};
document.querySelector('#close').onclick=()=>dialog.close();
let pendingPayload;
function cameraPayload(){refreshRTSPPaths();return{id:camera.id,name:document.querySelector('#name').value,use:document.querySelector('#use-camera').checked,address:addressInput.value,subnetMask:document.querySelector('#subnet-mask').value,gateway:document.querySelector('#gateway').value,manufacturer:camera.manufacturer,model:camera.model,username:usernameInput.value,password:passwordInput.value,mainStreamPath:mainRTSPInput.value,subStreamPath:subRTSPInput.value}}
async function submitCamera(payload){const response=await fetch('/api/ip-cameras',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});const result=await response.json().catch(()=>({}));if(response.status===401&&result.code==='credentials_required'){pendingPayload=payload;document.querySelector('#retry-username').value=payload.username||camera.username||'admin';document.querySelector('#retry-password').value='';credentialsDialog.showModal();return}if(!response.ok){alert(result.error||'Не удалось сохранить камеру');return}location.reload()}
document.querySelector('#save').onclick=()=>submitCamera(cameraPayload());
document.querySelector('#cancel-credentials').onclick=()=>credentialsDialog.close();
document.querySelector('#retry-save').onclick=()=>{pendingPayload.username=document.querySelector('#retry-username').value;pendingPayload.password=document.querySelector('#retry-password').value;credentialsDialog.close();submitCamera(pendingPayload)};
async function closeCameraPreviewSession(){const session=previewSession;previewSession=null;previewFrame.src='about:blank';previewMetadata.textContent='';previewStatus.textContent='';if(!session)return;try{await fetch('/api/ip-cameras/'+encodeURIComponent(session.cameraID)+'/preview/'+encodeURIComponent(session.sessionID),{method:'DELETE',keepalive:true})}catch(error){}}async function openCameraPreview(button){const cameraID=button.dataset.cameraPreview,stream=button.dataset.previewStream==='sub'?'sub':'main',details=button.closest('tr[data-stream-details]'),row=details?.previousElementSibling,name=row?.matches('tr[data-camera]')?(row.querySelector('td')?.textContent?.trim()||'IP-камера'):'IP-камера',streamLabel=stream==='sub'?'Sub stream':'Main stream';button.disabled=true;previewTitle.textContent=streamLabel+' · '+name;previewFrame.title='Предпросмотр '+streamLabel;renderCameraPreviewFallback(details,stream);previewStatus.textContent='Подготовка HLS-потока…';previewFrame.src='about:blank';previewDialog.showModal();try{const response=await fetch('/api/ip-cameras/'+encodeURIComponent(cameraID)+'/preview?stream='+encodeURIComponent(stream),{method:'POST'}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось запустить предпросмотр');if(!result.sessionId||!result.url)throw new Error('Сервис вернул неполные данные предпросмотра');previewSession={cameraID:cameraID,sessionID:result.sessionId};renderCameraPreviewMetadata(result.stream,stream);const playerURL=new URL(result.url);if(!/^https?:$/.test(playerURL.protocol)||playerURL.username||playerURL.password)throw new Error('Получен недопустимый адрес предпросмотра');if(!previewDialog.open){await closeCameraPreviewSession();return}playerURL.searchParams.set('controls','true');playerURL.searchParams.set('muted','true');playerURL.searchParams.set('autoplay','true');playerURL.searchParams.set('playsInline','true');previewFrame.src=playerURL.toString();previewStatus.textContent=''}catch(error){if(previewDialog.open)previewStatus.textContent=error.message}finally{button.disabled=false}}ipBody.onclick=event=>{const button=event.target.closest('[data-camera-preview]');if(button)openCameraPreview(button)};document.querySelector('#camera-preview-close').onclick=()=>previewDialog.close();previewDialog.addEventListener('close',()=>{void closeCameraPreviewSession()});document.addEventListener('click',async event=>{const button=event.target.closest('[data-camera-access]');if(!button)return;const popup=window.open('about:blank','_blank');button.disabled=true;try{const response=await fetch('/api/ip-cameras/'+encodeURIComponent(button.dataset.cameraAccess)+'/setup-access',{method:'POST'}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось открыть интерфейс камеры');if(popup)popup.location=result.url;else window.location=result.url}catch(error){if(popup)popup.close();alert(error.message)}finally{button.disabled=false}});
async function refreshIPCameras(){if(dialog.open||previewDialog.open||!document.hidden&&document.activeElement?.matches('input,select'))return;try{const html=await fetch('/ip-cameras',{cache:'no-store'}).then(r=>r.text());const next=new DOMParser().parseFromString(html,'text/html').querySelector('#ip-cameras-body');if(next){ipBody.replaceChildren(...next.children);loadTableVideoStreams()}}catch(error){}}const discoveryMechanisms=['Dahua — DHIP','UNV — XML probe UDP/3702 → 3705','UNV — ARP fallback'];function renderDiscoveryState(state){const labels={running:'Выполняется…',complete:'Завершено',error:'Ошибка'};const notice=document.querySelector('#refresh-notice');notice.innerHTML='<div class="discovery-title">Поиск новых камер</div>'+discoveryMechanisms.map(name=>'<div class="discovery-step"><span>'+name+'</span><span class="step-'+state+'">'+labels[state]+'</span></div>').join('');notice.hidden=false}async function discoverCameras(){const button=document.querySelector('#refresh-cameras');button.disabled=true;renderDiscoveryState('running');try{const response=await fetch('/api/ip-cameras/discover',{method:'POST'});if(!response.ok)throw new Error('Не удалось обновить список камер');renderDiscoveryState('complete');await new Promise(resolve=>setTimeout(resolve,900));location.replace('/ip-cameras?refreshed=1')}catch(error){renderDiscoveryState('error');button.disabled=false;setTimeout(()=>document.querySelector('#refresh-notice').hidden=true,4000)}}async function checkCameraStatus(){const notice=document.querySelector('#refresh-notice');notice.textContent='Проверка состояния камер…';notice.hidden=false;try{const response=await fetch('/api/ip-cameras/status',{method:'POST'});if(!response.ok)throw new Error('Не удалось проверить состояние камер');if(dialog.open||previewDialog.open){history.replaceState(null,'','/ip-cameras?refreshed=1');notice.hidden=true;return}location.replace('/ip-cameras?refreshed=1')}catch(error){notice.textContent=error.message;setTimeout(()=>notice.hidden=true,4000)}}document.querySelector('#refresh-cameras').onclick=discoverCameras;const cameraStatusRefreshed=new URLSearchParams(location.search).get('refreshed')==='1';setInterval(refreshIPCameras,5000);if(cameraStatusRefreshed)loadTableVideoStreams();else checkCameraStatus();
</script></body></html>`
