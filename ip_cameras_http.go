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

func deleteIPCameraHandler(service *ipcamera.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.Delete(r.PathValue("cameraID")); err != nil {
			if errors.Is(err, ipcamera.ErrCameraNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Камера не найдена"})
				return
			}
			log.Printf("delete IP camera: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось удалить камеру"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
			log.Printf("read IP camera video stream settings: %v", err)
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

const ipCamerasPageHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><script src="/assets/application-status.js" defer></script><link rel="stylesheet" href="/assets/application.css"><title>DronService — IP-камеры</title></head><body><div class="app-shell"><aside class="app-sidebar"><nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a href="/devices">Аналог. камеры</a><a class="active" href="/ip-cameras">IP-камеры</a><a href="/streams">MediaMTX</a><a href="/starlink">Starlink</a><a href="/zerotier">ZeroTier</a></nav><div id="network-info"><div id="network-addresses">Сеть: получение адресов…</div><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div></aside><main class="app-main"><div class="page-heading"><h1>Список IP-камер</h1><button id="refresh-cameras">Найти камеры</button></div><div id="refresh-notice" class="notice" hidden>Обновление списка камер…</div><div class="camera-grid" id="ip-cameras-body">{{range .}}<article class="camera-card" data-camera='{{. | json}}' data-camera-id="{{.ID}}"><div class="camera-card-header"><div class="camera-card-title">{{if .Name}}<span class="camera-name">{{.Name}}</span>{{else}}<span class="camera-name muted">Не настроена</span>{{end}}<span class="camera-address">{{.Address}}</span><div class="camera-card-meta"><span class="camera-vendor">{{.Manufacturer}}</span><span class="camera-model">{{if .Model}}{{.Model}}{{else}}Не определена{{end}}</span></div></div><div class="camera-card-controls"><span class="status-pill {{if .Online}}online{{else}}offline{{end}}" title="{{if .Online}}Активна{{else}}Неактивна{{end}}"><i class="status-dot {{if .Online}}online{{else}}offline{{end}}" aria-hidden="true"></i>{{if .Online}}Онлайн{{else}}Оффлайн{{end}}</span><span class="mediamtx-badge {{if .Use}}enabled{{else}}disabled{{end}}" title="{{if .Use}}Используется в MediaMTX{{else}}Не используется в MediaMTX{{end}}">MediaMTX</span></div></div><div class="stream-details-panel"><span class="stream-chip"><strong>Main</strong><span data-main-stream>{{if .MainStream.Resolution}}{{.MainStream.Resolution}}{{else}}—{{end}} · {{if .MainStream.FPS}}{{.MainStream.FPS}} FPS{{else}}—{{end}} · {{if .MainStream.BitrateKbps}}{{.MainStream.BitrateKbps}} кбит/с{{else}}—{{end}}</span><button type="button" class="btn-secondary-sm camera-link preview-button" data-camera-preview="{{.ID}}" data-preview-stream="main" aria-label="Открыть Main stream камеры {{if .Name}}{{.Name}}{{else}}{{.Address}}{{end}}">Просмотр ▶</button></span><span class="stream-chip"><strong>Sub</strong><span data-sub-stream>{{if .SubStream.Resolution}}{{.SubStream.Resolution}}{{else}}—{{end}} · {{if .SubStream.FPS}}{{.SubStream.FPS}} FPS{{else}}—{{end}} · {{if .SubStream.BitrateKbps}}{{.SubStream.BitrateKbps}} кбит/с{{else}}—{{end}}</span><button type="button" class="btn-secondary-sm camera-link preview-button" data-camera-preview="{{.ID}}" data-preview-stream="sub" aria-label="Открыть Sub stream камеры {{if .Name}}{{.Name}}{{else}}{{.Address}}{{end}}">Просмотр ▶</button></span></div><div class="camera-card-actions"><button type="button" class="btn-secondary-sm camera-link init-badge {{if eq .InitializationStatus "uninitialized"}}pending{{else if eq .InitializationStatus "initialized"}}ready{{end}}" data-camera-access="{{.ID}}">{{if eq .InitializationStatus "uninitialized"}}Авторизовать{{else if eq .InitializationStatus "initialized"}}Инициализирована · Открыть ↗{{else}}Неизвестно · Открыть ↗{{end}}</button><button type="button" class="btn-danger-sm danger" data-delete-ip-camera="{{.ID}}" aria-label="Удалить камеру {{if .Name}}{{.Name}}{{else}}{{.Address}}{{end}}">Удалить</button></div></article>{{end}}</div>
<dialog id="camera-dialog"><h2>Настройки IP-камеры</h2><p id="camera-device-info" class="camera-device-info"></p><div class="camera-name-row"><div class="camera-name-field"><label>Имя</label><input id="name" maxlength="100"></div><label class="media-toggle-label"><span>Использовать в MediaMTX</span><input id="use-camera" type="checkbox"></label></div><label>IP-адрес</label><input id="address"><div class="network-fields"><div class="network-field"><label>Маска подсети</label><input id="subnet-mask" inputmode="decimal"></div><div class="network-field"><label>Шлюз</label><input id="gateway" inputmode="decimal"></div></div><div class="credentials-fields"><div class="credential-field"><label for="username">Логин</label><input id="username" autocomplete="off"></div><div class="credential-field"><label for="password">Пароль</label><input id="password" type="password" autocomplete="new-password"></div></div><label>Main stream RTSP path</label><input id="main-rtsp"><label>Sub stream RTSP path</label><input id="sub-rtsp"><div class="actions"><button id="close">Закрыть</button> <button id="save">Сохранить</button></div></dialog><dialog id="credentials-dialog"><h2>Авторизация Dahua</h2><p>Сохранённые учётные данные не подошли. Укажите корректные данные камеры.</p><label>Логин</label><input id="retry-username" autocomplete="username"><label>Пароль</label><input id="retry-password" type="password" autocomplete="current-password"><div class="actions"><button id="cancel-credentials" type="button">Отмена</button> <button id="retry-save" type="button">Повторить</button></div></dialog><dialog id="camera-preview-dialog" class="preview-dialog" aria-labelledby="camera-preview-title" aria-describedby="camera-preview-metadata"><div class="preview-header"><h2 id="camera-preview-title">Предпросмотр</h2><button id="camera-preview-close" type="button">Закрыть</button></div><p id="camera-preview-metadata" class="preview-metadata" aria-live="polite"></p><iframe id="camera-preview-frame" class="preview-frame" title="Предпросмотр IP-камеры" scrolling="no" sandbox="allow-scripts allow-same-origin" allow="autoplay; fullscreen" referrerpolicy="no-referrer"></iframe><p id="camera-preview-status" class="preview-status" role="status" aria-live="polite"></p></dialog><script>
function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'],sl=s.starlink;if(sl?.internetViaStarlink){e.textContent='Starlink: ↓'+Number(sl.downlinkMbps||0).toFixed(2)+' ↑'+Number(sl.uplinkMbps||0).toFixed(2)+' Мбит/с · '+Number(sl.pingMs||s.pingMs||0).toFixed(1)+' мс';e.title='Подключение '+(sl.topology==='direct'?'напрямую к Starlink':'через роутер')+' · текущая скорость обмена'}else{e.textContent='Интернет: '+state[0]+(sl?.detected?' · Starlink найден':'')}e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}
updateInternetStatus();
const dialog=document.querySelector('#camera-dialog'),credentialsDialog=document.querySelector('#credentials-dialog'),deviceInfo=document.querySelector('#camera-device-info'),addressInput=document.querySelector('#address'),usernameInput=document.querySelector('#username'),passwordInput=document.querySelector('#password'),mainRTSPInput=document.querySelector('#main-rtsp'),subRTSPInput=document.querySelector('#sub-rtsp'),previewDialog=document.querySelector('#camera-preview-dialog'),previewFrame=document.querySelector('#camera-preview-frame'),previewTitle=document.querySelector('#camera-preview-title'),previewMetadata=document.querySelector('#camera-preview-metadata'),previewStatus=document.querySelector('#camera-preview-status');
let camera,previewSession=null;
const videoStreamRequests=new Map(),videoStreamsLoaded=new Set();
function rtspURLFromInputs(value){try{const parsed=new URL(value),address=addressInput.value.trim();if(address)parsed.hostname=address;parsed.username='';parsed.password='';return parsed.toString()}catch(error){return value}}
function refreshRTSPPaths(){mainRTSPInput.value=rtspURLFromInputs(mainRTSPInput.value);subRTSPInput.value=rtspURLFromInputs(subRTSPInput.value)}
function streamMetadata(stream){return[(stream?.resolution||'—'),(stream?.fps?stream.fps+' FPS':'—'),(stream?.bitrateKbps?stream.bitrateKbps+' кбит/с':'—')].join(' · ')}
function previewStreamLabel(kind){return kind==='sub'?'Sub':'Main'}
function renderCameraPreviewFallback(card,kind){const current=card?.querySelector(kind==='sub'?'[data-sub-stream]':'[data-main-stream]')?.textContent?.trim()||'— · — · —';previewMetadata.textContent='Поток: '+previewStreamLabel(kind)+' · '+current}
function renderCameraPreviewMetadata(value,fallbackKind){const kind=value?.kind==='sub'?'sub':value?.kind==='main'?'main':fallbackKind;previewMetadata.textContent='Поток: '+previewStreamLabel(kind)+' · Разрешение: '+(value?.resolution||'—')+' · FPS: '+(value?.fps||'—')+' · Битрейт: '+(value?.bitrateKbps?value.bitrateKbps+' кбит/с':'—')}
function isDahuaCamera(value){return(value.manufacturer||'')==='Dahua'||(value.protocol||'').split(',').some(protocol=>protocol.trim()==='DHIP')}
function isUNVCamera(value){return(value.manufacturer||'')==='UNV'||(value.protocol||'').split(',').some(protocol=>protocol.trim().startsWith('UNV-'))}
function supportsVideoStreamSettings(value){return isDahuaCamera(value)||isUNVCamera(value)}
function requestVideoStreams(value){if(!supportsVideoStreamSettings(value)||value.initializationStatus==='uninitialized')return Promise.resolve(value);if(videoStreamRequests.has(value.id))return videoStreamRequests.get(value.id);const request=(async()=>{try{const response=await fetch('/api/ip-cameras/'+encodeURIComponent(value.id)+'/video-streams',{method:'POST'}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось получить параметры потоков');videoStreamsLoaded.add(value.id);return result.camera}finally{videoStreamRequests.delete(value.id)}})();videoStreamRequests.set(value.id,request);return request}
function updateCameraRow(card,value){if(!card)return;card.querySelector('[data-main-stream]').textContent=streamMetadata(value.mainStream);card.querySelector('[data-sub-stream]').textContent=streamMetadata(value.subStream);card.dataset.camera=JSON.stringify(value);if(value.initializationStatus==='initialized'){const access=card.querySelector('[data-camera-access]');access.classList.remove('pending');access.classList.add('ready');access.textContent='Инициализирована · Открыть ↗'}}
function loadTableVideoStreams(){for(const card of ipBody.querySelectorAll('.camera-card[data-camera]')){let value;try{value=JSON.parse(card.dataset.camera)}catch(error){continue}if(!supportsVideoStreamSettings(value)||!value.hasPassword||value.initializationStatus==='uninitialized'||videoStreamsLoaded.has(value.id))continue;requestVideoStreams(value).then(refreshed=>{if(card.isConnected)updateCameraRow(card,refreshed)}).catch(error=>{for(const cell of card.querySelectorAll('[data-main-stream],[data-sub-stream]'))cell.title=error.message})}}
function openCamera(value){if(value.initializationStatus==='uninitialized'){alert('Сначала нажмите «Авторизовать» и завершите первичную инициализацию камеры.');return}camera=value;const manufacturer=camera.manufacturer||'Производитель не определён',model=camera.model||'модель не определена';deviceInfo.textContent=manufacturer+' · '+model;document.querySelector('#name').value=camera.name||'';document.querySelector('#use-camera').checked=!!camera.use;addressInput.value=camera.address||'';document.querySelector('#subnet-mask').value=camera.subnetMask||'';document.querySelector('#gateway').value=camera.gateway||'';usernameInput.value=camera.username||'';passwordInput.value='';passwordInput.placeholder=camera.hasPassword?'Пароль сохранён — оставьте пустым, чтобы не менять':'Введите пароль';mainRTSPInput.value=camera.mainStreamPath||'';subRTSPInput.value=camera.subStreamPath||'';refreshRTSPPaths();dialog.showModal()}
addressInput.addEventListener('input',refreshRTSPPaths);
for(const input of [mainRTSPInput,subRTSPInput])input.addEventListener('change',refreshRTSPPaths);
const ipBody=document.querySelector('#ip-cameras-body');
ipBody.addEventListener('click',async event=>{const button=event.target.closest('[data-delete-ip-camera]');if(!button)return;event.stopPropagation();if(!confirm('Удалить эту IP-камеру из списка?'))return;button.disabled=true;const response=await fetch('/api/ip-cameras/'+encodeURIComponent(button.dataset.deleteIpCamera),{method:'DELETE'});if(!response.ok){const result=await response.json().catch(()=>({}));alert(result.error||'Не удалось удалить камеру');button.disabled=false;return}location.reload()});
ipBody.ondblclick=event=>{if(event.target.closest('.toggle,.camera-link'))return;const card=event.target.closest('.camera-card[data-camera]');if(card)openCamera(JSON.parse(card.dataset.camera))};
document.querySelector('#close').onclick=()=>dialog.close();
let pendingPayload;
function cameraPayload(){refreshRTSPPaths();return{id:camera.id,name:document.querySelector('#name').value,use:document.querySelector('#use-camera').checked,address:addressInput.value,subnetMask:document.querySelector('#subnet-mask').value,gateway:document.querySelector('#gateway').value,manufacturer:camera.manufacturer,model:camera.model,username:usernameInput.value,password:passwordInput.value,mainStreamPath:mainRTSPInput.value,subStreamPath:subRTSPInput.value}}
async function submitCamera(payload){const response=await fetch('/api/ip-cameras',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});const result=await response.json().catch(()=>({}));if(response.status===401&&result.code==='credentials_required'){pendingPayload=payload;document.querySelector('#retry-username').value=payload.username||camera.username||'admin';document.querySelector('#retry-password').value='';credentialsDialog.showModal();return}if(!response.ok){alert(result.error||'Не удалось сохранить камеру');return}location.reload()}
document.querySelector('#save').onclick=()=>submitCamera(cameraPayload());
document.querySelector('#cancel-credentials').onclick=()=>credentialsDialog.close();
document.querySelector('#retry-save').onclick=()=>{pendingPayload.username=document.querySelector('#retry-username').value;pendingPayload.password=document.querySelector('#retry-password').value;credentialsDialog.close();submitCamera(pendingPayload)};
async function closeCameraPreviewSession(){const session=previewSession;previewSession=null;previewFrame.src='about:blank';previewMetadata.textContent='';previewStatus.textContent='';if(!session)return;try{await fetch('/api/ip-cameras/'+encodeURIComponent(session.cameraID)+'/preview/'+encodeURIComponent(session.sessionID),{method:'DELETE',keepalive:true})}catch(error){}}async function openCameraPreview(button){const cameraID=button.dataset.cameraPreview,stream=button.dataset.previewStream==='sub'?'sub':'main',card=button.closest('.camera-card[data-camera]'),name=card?(card.querySelector('.camera-name:not(.muted)')?.textContent?.trim()||card.querySelector('.camera-address')?.textContent?.trim()||'IP-камера'):'IP-камера',streamLabel=stream==='sub'?'Sub stream':'Main stream';button.disabled=true;previewTitle.textContent=streamLabel+' · '+name;previewFrame.title='Предпросмотр '+streamLabel;renderCameraPreviewFallback(card,stream);previewStatus.textContent='Подготовка HLS-потока…';previewFrame.src='about:blank';previewDialog.showModal();try{const response=await fetch('/api/ip-cameras/'+encodeURIComponent(cameraID)+'/preview?stream='+encodeURIComponent(stream),{method:'POST'}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось запустить предпросмотр');if(!result.sessionId||!result.url)throw new Error('Сервис вернул неполные данные предпросмотра');previewSession={cameraID:cameraID,sessionID:result.sessionId};renderCameraPreviewMetadata(result.stream,stream);const playerURL=new URL(result.url);if(!/^https?:$/.test(playerURL.protocol)||playerURL.username||playerURL.password)throw new Error('Получен недопустимый адрес предпросмотра');if(!previewDialog.open){await closeCameraPreviewSession();return}playerURL.searchParams.set('controls','true');playerURL.searchParams.set('muted','true');playerURL.searchParams.set('autoplay','true');playerURL.searchParams.set('playsInline','true');previewFrame.src=playerURL.toString();previewStatus.textContent=''}catch(error){if(previewDialog.open)previewStatus.textContent=error.message}finally{button.disabled=false}}ipBody.onclick=event=>{const button=event.target.closest('[data-camera-preview]');if(button)openCameraPreview(button)};document.querySelector('#camera-preview-close').onclick=()=>previewDialog.close();previewDialog.addEventListener('close',()=>{void closeCameraPreviewSession()});document.addEventListener('click',async event=>{const button=event.target.closest('[data-camera-access]');if(!button)return;const popup=window.open('about:blank','_blank');button.disabled=true;try{const response=await fetch('/api/ip-cameras/'+encodeURIComponent(button.dataset.cameraAccess)+'/setup-access',{method:'POST'}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось открыть интерфейс камеры');if(popup)popup.location=result.url;else window.location=result.url}catch(error){if(popup)popup.close();alert(error.message)}finally{button.disabled=false}});
async function refreshIPCameras(){if(dialog.open||previewDialog.open||!document.hidden&&document.activeElement?.matches('input,select'))return;try{const html=await fetch('/ip-cameras',{cache:'no-store'}).then(r=>r.text());const next=new DOMParser().parseFromString(html,'text/html').querySelector('#ip-cameras-body');if(next){ipBody.replaceChildren(...next.children);loadTableVideoStreams()}}catch(error){}}const discoveryMechanisms=['Dahua — DHIP','UNV — XML probe UDP/3702 → 3705','UNV — ARP fallback'];function renderDiscoveryState(state){const labels={running:'Выполняется…',complete:'Завершено',error:'Ошибка'};const notice=document.querySelector('#refresh-notice');notice.innerHTML='<div class="discovery-title">Поиск новых камер</div>'+discoveryMechanisms.map(name=>'<div class="discovery-step"><span>'+name+'</span><span class="step-'+state+'">'+labels[state]+'</span></div>').join('');notice.hidden=false}async function discoverCameras(){const button=document.querySelector('#refresh-cameras');button.disabled=true;renderDiscoveryState('running');try{const response=await fetch('/api/ip-cameras/discover',{method:'POST'});if(!response.ok)throw new Error('Не удалось обновить список камер');renderDiscoveryState('complete');await new Promise(resolve=>setTimeout(resolve,900));location.replace('/ip-cameras?refreshed=1')}catch(error){renderDiscoveryState('error');button.disabled=false;setTimeout(()=>document.querySelector('#refresh-notice').hidden=true,4000)}}async function checkCameraStatus(){const notice=document.querySelector('#refresh-notice');notice.textContent='Проверка состояния камер…';notice.hidden=false;try{const response=await fetch('/api/ip-cameras/status',{method:'POST'});if(!response.ok)throw new Error('Не удалось проверить состояние камер');if(dialog.open||previewDialog.open){history.replaceState(null,'','/ip-cameras?refreshed=1');notice.hidden=true;return}location.replace('/ip-cameras?refreshed=1')}catch(error){notice.textContent=error.message;setTimeout(()=>notice.hidden=true,4000)}}document.querySelector('#refresh-cameras').onclick=discoverCameras;const cameraStatusRefreshed=new URLSearchParams(location.search).get('refreshed')==='1';setInterval(refreshIPCameras,5000);if(cameraStatusRefreshed)loadTableVideoStreams();else checkCameraStatus();
</script></main></div></body></html>`
