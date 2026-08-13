package main

import (
	"log"
	"net/http"

	"DronService/internal/buildinfo"
	"DronService/internal/updater"
)

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, buildinfo.Current())
}

func applicationUpdateStatusHandler(client *updater.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, client.Status(r.Context()))
	}
}

func applicationUpdateRequestHandler(client *updater.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := client.Request(r.Context()); err != nil {
			log.Printf("request DronService update: %v", err)
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Обновление сейчас недоступно"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"updating": true})
	}
}

func applicationStatusScriptHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(applicationStatusScript))
}

const applicationStatusScript = `(()=>{
const versionElement=document.querySelector('#app-version'),button=document.querySelector('#update-app'),stateElement=document.querySelector('#update-app-state');
if(!versionElement||!button||!stateElement)return;
let polling=false;
const stateLabels={pending:'Ожидание установки…',downloading:'Скачивание…',verifying:'Проверка подписи…',installing:'Установка…',restarting:'Перезапуск…',failed:'Ошибка обновления'};
async function load(){
  try{
    const [versionResponse,statusResponse]=await Promise.all([fetch('/api/version',{cache:'no-store'}),fetch('/api/update/status',{cache:'no-store'})]);
    if(!versionResponse.ok||!statusResponse.ok)throw new Error('status unavailable');
    const version=await versionResponse.json(),status=await statusResponse.json();
    versionElement.textContent=version.version||'dev';
    button.hidden=!status.updateAvailable||status.installing;
    button.textContent=status.latestVersion?'Обновить до '+status.latestVersion:'Обновить';
    stateElement.textContent=status.installing||status.state==='failed'?(stateLabels[status.state]||'Обновление…'):'';
    if(status.installing){polling=true;setTimeout(load,2000);return}
    if(polling&&status.state==='succeeded'){polling=false;setTimeout(()=>location.reload(),700)}
  }catch(error){if(polling)setTimeout(load,2000)}
}
button.addEventListener('click',async()=>{
  if(!confirm('Установить новую версию DronService? Сервис будет перезапущен.'))return;
  button.disabled=true;button.hidden=true;stateElement.textContent='Запуск обновления…';polling=true;
  try{const response=await fetch('/api/update',{method:'POST'});if(!response.ok){const result=await response.json().catch(()=>({}));throw new Error(result.error||'Не удалось запустить обновление')}setTimeout(load,1000)}
  catch(error){polling=false;button.disabled=false;button.hidden=false;stateElement.textContent=error.message}
});
load();
})();`
