package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"

	"DronService/internal/mediamtx"
	"DronService/internal/stream"
	"DronService/internal/zerotier"
)

var streamNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func streamConfigsHandler(service *stream.Service, catalog streamSourceCatalog, publicRTSPBase string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			configs, err := service.ListConfigs(r.Context())
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream configuration unavailable"})
				return
			}
			sources, err := catalog.List(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "camera sources unavailable"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"streams": decorateStreamConfigs(configs, sources, publicRTSPBase), "sources": sources})
		case http.MethodPost, http.MethodPatch:
			var request struct {
				Name         string `json:"name"`
				OriginalName string `json:"originalName"`
				SourceID     string `json:"sourceId"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&request) != nil || !streamNamePattern.MatchString(request.Name) || strings.TrimSpace(request.SourceID) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid stream configuration"})
				return
			}
			existingName := ""
			if r.Method == http.MethodPatch {
				existingName = request.OriginalName
				if existingName == "" {
					existingName = request.Name
				}
				if !streamNamePattern.MatchString(existingName) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid original stream name"})
					return
				}
			}
			source, err := catalog.Resolve(r.Context(), request.SourceID)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			config := stream.Config{Name: request.Name, SourceID: request.SourceID}
			err = service.ApplySource(r.Context(), config, source, existingName)
			if err != nil {
				log.Printf("change stream configuration: %v", err)
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream configuration could not be changed"})
				return
			}
			config.SourceName, config.SourceType = source.Name, source.Type
			config.RTSPPath = strings.TrimRight(publicRTSPBase, "/") + "/" + config.Name
			writeJSON(w, http.StatusOK, config)
		case http.MethodDelete:
			name := r.URL.Query().Get("name")
			if !streamNamePattern.MatchString(name) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid stream name"})
				return
			}
			if err := service.DeleteConfig(r.Context(), name); err != nil {
				log.Printf("delete stream configuration: %v", err)
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream could not be deleted"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func mediaMTXInstallHandler(installer *mediamtx.Installer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			status, err := installer.Status(r.Context())
			if err != nil {
				log.Printf("check MediaMTX installation: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "MediaMTX status unavailable"})
				return
			}
			writeJSON(w, http.StatusOK, status)
		case http.MethodPost:
			if err := installer.Request(r.Context()); err != nil {
				log.Printf("request MediaMTX installation: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "MediaMTX installation could not be started"})
				return
			}
			writeJSON(w, http.StatusAccepted, mediamtx.InstallStatus{Installing: true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

type streamPageConfig struct {
	stream.Config
	RTSPPaths []string
}

func newStreamsPageHandler(service *stream.Service, catalog streamSourceCatalog, installer *mediamtx.Installer, zeroTierClient *zerotier.Client, publicRTSPBase string) (http.Handler, error) {
	page, err := template.New("streams").Parse(streamsPageHTML)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installStatus, err := installer.Status(r.Context())
		if err != nil {
			http.Error(w, "MediaMTX status unavailable", http.StatusInternalServerError)
			return
		}
		var configs []stream.Config
		if installStatus.Installed {
			configs, err = service.ListConfigs(r.Context())
			if err != nil {
				http.Error(w, "stream service unavailable", http.StatusBadGateway)
				return
			}
		}
		sources, err := catalog.List(r.Context())
		if err != nil {
			http.Error(w, "camera sources unavailable", http.StatusInternalServerError)
			return
		}
		configs = decorateStreamConfigs(configs, sources, publicRTSPBase)
		host := preferredRTSPHost()
		zeroTierIP := ""
		if snapshot, snapshotErr := zeroTierClient.Snapshot(r.Context()); snapshotErr == nil && snapshot.Status.Online {
			for _, network := range snapshot.Networks {
				if network.Status != "OK" {
					continue
				}
				for _, address := range network.AssignedAddresses {
					ip, _, parseErr := net.ParseCIDR(address)
					if parseErr == nil && ip.To4() != nil {
						zeroTierIP = ip.String()
						break
					}
				}
				if zeroTierIP != "" {
					break
				}
			}
		}
		pageConfigs := make([]streamPageConfig, 0, len(configs))
		for _, config := range configs {
			paths := []string{"rtsp://" + net.JoinHostPort(host, "554") + "/" + config.Name}
			if zeroTierIP != "" && zeroTierIP != host {
				paths = append(paths, "rtsp://"+net.JoinHostPort(zeroTierIP, "554")+"/"+config.Name)
			}
			pageConfigs = append(pageConfigs, streamPageConfig{Config: config, RTSPPaths: paths})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := page.Execute(w, struct {
			Configs        []streamPageConfig
			Sources        []stream.Source
			PublicRTSPBase string
			MediaMTX       mediamtx.InstallStatus
		}{Configs: pageConfigs, Sources: sources, PublicRTSPBase: "rtsp://" + net.JoinHostPort(host, "554"), MediaMTX: installStatus}); err != nil {
			log.Printf("render streams page: %v", err)
		}
	}), nil
}

func preferredRTSPHost() string {
	interfaces, err := networkInterfaceSnapshots()
	if err != nil {
		return "127.0.0.1"
	}
	return preferredRTSPHostFromStatus(buildNetworkStatus(interfaces, ""))
}

func preferredRTSPHostFromStatus(status networkStatusResponse) string {
	if len(status.LAN) > 0 {
		return status.LAN[0]
	}
	if len(status.WiFi) > 0 {
		return status.WiFi[0]
	}
	return "127.0.0.1"
}

const streamsPageHTML = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><script src="/assets/application-status.js" defer></script><link rel="stylesheet" href="/assets/application.css"><title>DronService — стримы</title><style>
:root{color-scheme:dark;font-family:system-ui,sans-serif}body{max-width:1100px;margin:40px auto;padding:0 20px;background:#111827;color:#e5e7eb}.main-nav{display:flex;align-items:center;gap:8px;margin-bottom:32px;padding:10px;border:1px solid #374151;border-radius:12px;background:#1f2937;box-shadow:0 8px 24px rgb(0 0 0 / 20%)}.main-nav .brand{margin:0 auto 0 6px;color:#f9fafb;font-weight:700;letter-spacing:.02em}.main-nav a{padding:9px 13px;border-radius:8px;color:#cbd5e1;text-decoration:none;transition:background .15s,color .15s}.main-nav a:hover,.main-nav a:focus-visible{background:#374151;color:#fff;outline:none}.main-nav a.active{background:#2563eb;color:#fff}@media(max-width:700px){.main-nav{align-items:stretch;flex-direction:column}.main-nav .brand{margin:4px 8px 8px}}.page-heading{display:flex;align-items:center;justify-content:space-between;gap:20px}.page-heading h1{margin:0}@media(max-width:600px){.page-heading{align-items:stretch;flex-direction:column;gap:12px}.page-heading button{align-self:flex-start}}table{width:100%;border-collapse:collapse;background:#1f2937;margin-top:20px}th,td{padding:12px;border-bottom:1px solid #374151;text-align:left}tbody tr{cursor:pointer}tbody tr:hover{background:#273449}code{color:#a7f3d0}button,input,select{padding:10px;border-radius:6px;border:1px solid #4b5563}input,select{box-sizing:border-box;width:100%;background:#111827;color:#fff}label{display:block;margin:14px 0 5px}dialog{width:min(620px,calc(100% - 40px));background:#1f2937;color:#fff;border:1px solid #4b5563;border-radius:10px}.field-label{display:flex;align-items:center;gap:7px;margin-top:14px}.field-label label{margin:0}.info-button{width:20px;height:20px;padding:0;border-radius:50%;line-height:18px;font-size:12px;font-weight:700}.info-dialog{width:min(420px,calc(100% - 40px))}.actions{text-align:right;margin-top:20px}.danger{background:#b91c1c;color:#fff}.empty,.setup{padding:20px;background:#1f2937;border-radius:8px;margin-top:20px}.setup button{margin-top:8px}.type{color:#93c5fd}.source-display{display:flex;align-items:center;gap:8px}.source-icon{display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;flex:0 0 24px;border:1px solid #4b5563;border-radius:6px;font-size:14px}.source-detail{color:#9ca3af;font-size:.75rem}.rtsp-line{display:flex;align-items:center;gap:8px;margin:4px 0}.rtsp-line code{flex:1}.copy-path{padding:5px 8px;white-space:nowrap}
</style></head><body data-rtsp-base="{{.PublicRTSPBase}}"><nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a href="/devices">Аналог. камеры</a><a href="/ip-cameras">IP-камеры</a><a class="active" href="/streams">MediaMTX</a><a href="/zerotier">ZeroTier</a></nav><div id="network-info" style="display:flex;align-items:center;gap:10px;margin:-22px 6px 28px;color:#9ca3af;font-size:.9rem"><span id="network-addresses">Сеть: получение адресов…</span><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div><div class="page-heading"><h1>MediaMTX{{if .MediaMTX.Version}} · v{{.MediaMTX.Version}}{{end}}</h1>{{if .MediaMTX.Installed}}<button id="add" {{if not .Sources}}disabled{{end}}>Добавить стрим</button>{{end}}</div>{{if not .MediaMTX.Installed}}<section class="setup"><h2>MediaMTX не установлен</h2><p>Установите MediaMTX для управления RTSP-потоками. После установки сервис будет запускаться автоматически.</p><button id="install-mediamtx" {{if .MediaMTX.Installing}}disabled{{end}}>{{if .MediaMTX.Installing}}Установка…{{else}}Установить MediaMTX{{end}}</button></section><script>const installButton=document.querySelector('#install-mediamtx');async function waitForInstallation(){const status=await fetch('/api/mediamtx/install').then(r=>r.json());if(status.installed){location.reload();return}setTimeout(waitForInstallation,1500)}if(installButton){if(installButton.disabled)waitForInstallation();installButton.onclick=async()=>{installButton.disabled=true;installButton.textContent='Установка…';const response=await fetch('/api/mediamtx/install',{method:'POST'});if(!response.ok){alert((await response.json()).error);installButton.disabled=false;installButton.textContent='Установить MediaMTX';return}waitForInstallation()}}</script>{{else}}{{if .MediaMTX.UpdateAvailable}}<section class="setup"><button id="update-mediamtx">Обновить MediaMTX</button></section>{{end}}{{if not .Sources}}<p class="empty">Сначала отметьте аналоговую или IP-камеру как «Использовать».</p>{{end}}<table><thead><tr><th>Имя</th><th>Источник</th><th>Обработка</th><th>RTSP path</th></tr></thead><tbody id="streams-body">{{range .Configs}}<tr data-name="{{.Name}}" data-source-id="{{.SourceID}}"><td>{{.Name}}</td><td><span class="source-display"><span class="source-icon" title="{{if eq .SourceType "ip"}}IP-камера{{else}}Аналоговая камера{{end}}">{{if eq .SourceType "ip"}}🌐{{else}}📹{{end}}</span><span>{{if .SourceName}}{{.SourceName}}{{else}}{{.Source}}{{end}} {{if .SourceDetail}}<small class="source-detail">({{.SourceDetail}})</small>{{end}}</span></span></td><td class="type">{{if eq .SourceType "ip"}}Proxy{{else if eq .SourceType "analog"}}H.264{{else}}Не определён{{end}}</td><td>{{range .RTSPPaths}}<div class="rtsp-line"><code>{{.}}</code><button type="button" class="copy-path" data-copy="{{.}}" title="Копировать RTSP path" aria-label="Копировать RTSP path">&#128203;</button></div>{{end}}</td></tr>{{end}}</tbody></table>
<dialog id="stream-dialog"><h2>Настройки стрима</h2><div class="field-label"><label for="name">Имя стрима</label><button id="stream-name-info" class="info-button" type="button" aria-label="Ограничения имени стрима">i</button></div><input id="name" maxlength="100" pattern="[A-Za-z0-9_-]+" title="Только латинские буквы, цифры, дефис и подчёркивание; без пробелов" required autocomplete="off" spellcheck="false"><label>Источник</label><select id="source"><option value="">Выберите камеру</option>{{range .Sources}}<option value="{{.ID}}" data-type="{{.Type}}">{{if eq .Type "analog"}}📹{{else}}🌐{{end}} {{.Name}}{{if .Detail}} ({{.Detail}}){{end}}</option>{{end}}</select><label>Обработка</label><input id="processing" readonly><label>Полученный RTSP path</label><input id="rtsp-path" readonly><div class="actions"><button id="remove" class="danger">Удалить</button> <button id="close">Закрыть</button> <button id="save">Сохранить</button></div></dialog><dialog id="stream-name-info-dialog" class="info-dialog"><h3>Ограничения имени стрима</h3><p>Используйте только латинские буквы A–Z, цифры, дефис (-) и подчёркивание (_). Пробелы недопустимы.</p><div class="actions"><button id="stream-name-info-close" type="button">Понятно</button></div></dialog><script>
function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'];e.textContent='Интернет: '+state[0];e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}updateInternetStatus();function updateNetworkInfo(){fetch('/api/system/network').then(r=>{if(!r.ok)throw new Error('network unavailable');return r.json()}).then(s=>{const parts=['LAN: '+(s.lan?.join(', ')||'—'),'Wi-Fi: '+(s.wifi?.join(', ')||'—')];if(s.localName)parts.push('Имя: '+s.localName);document.querySelector('#network-addresses').textContent=parts.join(' · ')}).catch(()=>document.querySelector('#network-addresses').textContent='Сеть: адреса недоступны')}updateNetworkInfo();const updateButton=document.querySelector('#update-mediamtx');if(updateButton)updateButton.onclick=async()=>{updateButton.disabled=true;updateButton.textContent='Обновление…';const r=await fetch('/api/mediamtx/install',{method:'POST'});if(!r.ok){alert((await r.json()).error);updateButton.disabled=false;return}const poll=async()=>{const s=await fetch('/api/mediamtx/install').then(r=>r.json());if(!s.installing&&!s.updateAvailable){location.reload();return}setTimeout(poll,1500)};setTimeout(poll,1500)};const dialog=document.querySelector('#stream-dialog'),streamNameInfoDialog=document.querySelector('#stream-name-info-dialog'),nameInput=document.querySelector('#name'),sourceSelect=document.querySelector('#source'),processing=document.querySelector('#processing'),rtspPath=document.querySelector('#rtsp-path'),remove=document.querySelector('#remove'),rtspBase=document.body.dataset.rtspBase;let editing=false,originalName='';
function refreshDetails(){const type=sourceSelect.selectedOptions[0]?.dataset.type;processing.value=type==='analog'?'H.264':type==='ip'?'Proxy':'';rtspPath.value=nameInput.value?rtspBase.replace(/\/$/,'')+'/'+nameInput.value:''}
function openEditor(row){editing=!!row;originalName=row?.dataset.name||'';nameInput.value=originalName;nameInput.disabled=false;sourceSelect.value=row?.dataset.sourceId||'';remove.hidden=!editing;refreshDetails();dialog.showModal()}
document.querySelector('#streams-body').onclick=async event=>{const button=event.target.closest('.copy-path');if(!button)return;event.stopPropagation();try{if(navigator.clipboard){await navigator.clipboard.writeText(button.dataset.copy)}else{const area=document.createElement('textarea');area.value=button.dataset.copy;area.style.position='fixed';area.style.opacity='0';document.body.appendChild(area);area.select();if(!document.execCommand('copy'))throw new Error('copy failed');area.remove()}const old=button.textContent;button.textContent='✓';setTimeout(()=>button.textContent=old,1200)}catch(error){alert('Не удалось скопировать RTSP path')}};document.querySelector('#streams-body').ondblclick=event=>{const row=event.target.closest('tr[data-name]');if(row)openEditor(row)};async function refreshStreams(){if(dialog.open)return;try{const html=await fetch('/streams',{cache:'no-store'}).then(r=>r.text());const next=new DOMParser().parseFromString(html,'text/html').querySelector('#streams-body');if(next)document.querySelector('#streams-body').replaceChildren(...next.children)}catch(error){}}setInterval(refreshStreams,5000);nameInput.oninput=refreshDetails;sourceSelect.onchange=refreshDetails;document.querySelector('#stream-name-info').onclick=()=>streamNameInfoDialog.showModal();document.querySelector('#stream-name-info-close').onclick=()=>streamNameInfoDialog.close();document.querySelector('#add').onclick=()=>openEditor();document.querySelector('#close').onclick=()=>dialog.close();document.querySelector('#save').onclick=async()=>{if(!nameInput.checkValidity()){nameInput.reportValidity();return}if(!sourceSelect.value){alert('Выберите источник');return}const response=await fetch('/api/stream-configs',{method:editing?'PATCH':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:nameInput.value,originalName:originalName,sourceId:sourceSelect.value})});if(!response.ok){alert((await response.json()).error);return}location.reload()};remove.onclick=async()=>{const name=originalName||nameInput.value;if(!confirm('Удалить стрим '+name+'?'))return;const response=await fetch('/api/stream-configs?name='+encodeURIComponent(name),{method:'DELETE'});if(!response.ok){alert('Не удалось удалить стрим');return}location.reload()};
</script>{{end}}</body></html>`
