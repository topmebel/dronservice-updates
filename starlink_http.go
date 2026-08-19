package main

import (
	"net/http"

	"DronService/internal/starlink"
)

func starlinkStatusHandler(service *starlink.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		internet := internetChecker.Status(r.Context())
		writeJSON(w, http.StatusOK, service.Snapshot(r.Context(), internet.Online, r.URL.Query().Get("range")))
	}
}

func starlinkPageHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(starlinkPageHTML))
}

const starlinkPageHTML = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <script src="/assets/application-status.js" defer></script>
  <link rel="stylesheet" href="/assets/application.css">
  <title>DronService — Starlink</title>
  <style>
    .node-card{display:flex;flex-wrap:wrap;gap:28px;margin:22px 0;padding:18px}.node-item span{display:block;margin-bottom:4px;color:#9ca3af;font-size:.85rem}.status-line{display:flex;align-items:center;gap:9px}.status-dot{display:inline-block;width:12px;height:12px;border-radius:50%;background:#6b7280}.status-dot.online{background:#22c55e}.status-dot.offline{background:#ef4444}.alerts{border-color:#7f1d1d;color:#fecaca;margin:18px 0;padding:20px}.log-error{color:#fca5a5}.log-warning{color:#fde68a}.muted{color:#9ca3af}
  </style>
</head>
<body>
<div class="app-shell">
<aside class="app-sidebar">
  <nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a href="/devices">Аналог. камеры</a><a href="/ip-cameras">IP-камеры</a><a href="/streams">MediaMTX</a><a class="active" href="/starlink">Starlink</a><a href="/zerotier">ZeroTier</a></nav>
  <div id="network-info"><div id="network-addresses">Сеть: получение адресов…</div><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div>
</aside>
<main class="app-main">
  <div class="page-heading"><h1>Starlink</h1><button id="reboot-starlink" type="button" disabled>Перезагрузить Starlink</button></div>
  <div id="content"><p class="empty">Получение состояния Starlink…</p></div>
  <script>
    function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'],sl=s.starlink;if(sl?.internetViaStarlink){e.textContent='Starlink: ↓'+Number(sl.downlinkMbps||0).toFixed(2)+' ↑'+Number(sl.uplinkMbps||0).toFixed(2)+' Мбит/с · '+Number(sl.pingMs||s.pingMs||0).toFixed(1)+' мс';e.title='Подключение '+(sl.topology==='direct'?'напрямую к Starlink':'через роутер')+' · текущая скорость обмена'}else{e.textContent='Интернет: '+state[0]+(sl?.detected?' · Starlink найден':'')}e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}updateInternetStatus();
    const content=document.querySelector('#content');
    const chartRanges=[{id:'10m',label:'10 мин'},{id:'1h',label:'1 ч'},{id:'24h',label:'сутки'},{id:'7d',label:'неделя'}];
    let chartRange=localStorage.getItem('starlink-chart-range')||'1h';
    let latestHistory=[];
    let latestChartLayout=null;
    const escapeHTML=value=>String(value??'').replace(/[&<>'"]/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[char]));
    const topologyLabel=value=>value==='direct'?'напрямую':'через роутер';
    const formatTime=value=>{try{return new Date(value).toLocaleString('ru-RU')}catch(error){return value||'—'}}
    function formatAxisTime(value){const date=new Date(value);if(chartRange==='10m')return date.toLocaleTimeString('ru-RU',{hour:'2-digit',minute:'2-digit',second:'2-digit'});if(chartRange==='7d')return date.toLocaleDateString('ru-RU',{day:'2-digit',month:'2-digit'})+' '+date.toLocaleTimeString('ru-RU',{hour:'2-digit',minute:'2-digit'});return date.toLocaleTimeString('ru-RU',{hour:'2-digit',minute:'2-digit'})}
    function renderMetricsChart(history){
      const canvas=document.querySelector('#metrics-chart');
      if(!canvas)return;
      const points=(history||[]).slice().sort((a,b)=>new Date(a.time)-new Date(b.time));
      latestHistory=points;
      const ratio=window.devicePixelRatio||1;
      const width=canvas.clientWidth||640;
      const height=canvas.clientHeight||240;
      canvas.width=Math.max(1,Math.floor(width*ratio));
      canvas.height=Math.max(1,Math.floor(height*ratio));
      const ctx=canvas.getContext('2d');
      ctx.setTransform(ratio,0,0,ratio,0,0);
      ctx.clearRect(0,0,width,height);
      if(points.length<2){
        latestChartLayout=null;
        ctx.fillStyle='#94a3b8';
        ctx.font='13px sans-serif';
        ctx.fillText('Недостаточно данных — график появится через несколько минут',16,32);
        return;
      }
      const pad={t:20,r:52,b:36,l:52};
      const plotW=width-pad.l-pad.r;
      const plotH=height-pad.t-pad.b;
      const minT=new Date(points[0].time).getTime();
      const maxT=new Date(points[points.length-1].time).getTime();
      const x=t=>pad.l+((t-minT)/(maxT-minT||1))*plotW;
      const down=points.map(p=>Number(p.downlinkMbps||0));
      const up=points.map(p=>Number(p.uplinkMbps||0));
      const ping=points.map(p=>Number(p.pingMs||0));
      const maxMbps=Math.max(1,...down,...up);
      const maxPing=Math.max(1,...ping);
      const yMbps=v=>pad.t+plotH-(v/maxMbps)*plotH;
      const yPing=v=>pad.t+plotH-(v/maxPing)*plotH;
      ctx.strokeStyle='rgb(71 85 105 / 35%)';
      ctx.lineWidth=1;
      for(let i=0;i<=4;i++){
        const yy=pad.t+(plotH/4)*i;
        ctx.beginPath();ctx.moveTo(pad.l,yy);ctx.lineTo(width-pad.r,yy);ctx.stroke();
      }
      function drawSeries(values,color,yScale){
        ctx.strokeStyle=color;ctx.lineWidth=2.5;ctx.beginPath();
        values.forEach((value,index)=>{
          const px=x(new Date(points[index].time).getTime()),py=yScale(value);
          if(index===0)ctx.moveTo(px,py);else ctx.lineTo(px,py);
        });
        ctx.stroke();
      }
      drawSeries(down,'#3b82f6',yMbps);
      drawSeries(up,'#34d399',yMbps);
      drawSeries(ping,'#fbbf24',yPing);
      ctx.fillStyle='#94a3b8';
      ctx.font='11px sans-serif';
      ctx.fillText('0',pad.l-8,pad.t+plotH+4);
      ctx.fillText(maxMbps.toFixed(1)+' Мбит/с',4,pad.t+10);
      ctx.textAlign='right';
      ctx.fillText(maxPing.toFixed(0)+' мс',width-4,pad.t+10);
      ctx.textAlign='left';
      const startLabel=formatAxisTime(minT);
      const endLabel=formatAxisTime(maxT);
      ctx.fillText(startLabel,pad.l,height-10);
      ctx.textAlign='right';
      ctx.fillText(endLabel,width-pad.r,height-10);
      ctx.textAlign='left';
      latestChartLayout={pad,minT,maxT,plotW,plotH,points,width,height};
    }
    function bindChartHover(){
      const canvas=document.querySelector('#metrics-chart');
      const tooltip=document.querySelector('#metrics-chart-tooltip');
      if(!canvas||!tooltip)return;
      canvas.onmousemove=event=>{
        const layout=latestChartLayout;
        if(!layout||layout.points.length<2){tooltip.hidden=true;return}
        const rect=canvas.getBoundingClientRect();
        const mx=event.clientX-rect.left;
        const my=event.clientY-rect.top;
        const {pad,minT,maxT,plotW,plotH,points,width,height}=layout;
        if(mx<pad.l||mx>width-pad.r||my<pad.t||my>height-pad.b){tooltip.hidden=true;return}
        const t=minT+((mx-pad.l)/plotW)*(maxT-minT);
        let best=0,bestDist=Infinity;
        points.forEach((point,index)=>{const dist=Math.abs(new Date(point.time).getTime()-t);if(dist<bestDist){bestDist=dist;best=index}});
        tooltip.textContent=formatTime(points[best].time);
        tooltip.hidden=false;
        tooltip.style.left=Math.min(Math.max(mx+12,8),Math.max(8,width-tooltip.offsetWidth-8))+'px';
        tooltip.style.top=Math.max(my-32,8)+'px';
      };
      canvas.onmouseleave=()=>{tooltip.hidden=true};
    }
    function render(data){
      const online=!!data.reachable,via=!!data.internetViaStarlink;
      let html='<div class="chart-range" role="group" aria-label="Диапазон графика">'+chartRanges.map(range=>'<button type="button" data-chart-range="'+range.id+'" class="'+(chartRange===range.id?'active':'')+'">'+range.label+'</button>').join('')+'</div><div class="chart-wrap"><canvas id="metrics-chart" class="connection-chart"></canvas><div id="metrics-chart-tooltip" class="chart-tooltip" hidden></div></div><div class="chart-legend" aria-label="Расшифровка линий графика"><span class="chart-legend-title">Линии на графике</span><span class="legend-item legend-downlink"><i class="legend-line" aria-hidden="true"></i> загрузка (↓ Мбит/с)</span><span class="legend-item legend-uplink"><i class="legend-line" aria-hidden="true"></i> выгрузка (↑ Мбит/с)</span><span class="legend-item legend-ping"><i class="legend-line" aria-hidden="true"></i> пинг (мс)</span></div>';
      html+='<div class="node-card"><div class="node-item"><span>Терминал</span><div class="status-line"><i class="status-dot '+(online?'online':'offline')+'"></i>'+(online?'Доступен':'Недоступен')+'</div></div><div class="node-item"><span>Интернет</span><strong>'+(via?'через Starlink':(data.detected?'не через Starlink':'нет'))+'</strong></div><div class="node-item"><span>Подключение</span><strong>'+escapeHTML(topologyLabel(data.topology))+'</strong></div><div class="node-item"><span>Состояние</span><strong>'+escapeHTML(data.state||'—')+'</strong></div><div class="node-item"><span>Скорость</span><strong>↓ '+Number(data.downlinkMbps||0).toFixed(2)+' · ↑ '+Number(data.uplinkMbps||0).toFixed(2)+' Мбит/с</strong></div><div class="node-item"><span>Пинг</span><strong>'+Number(data.pingMs||0).toFixed(1)+' мс</strong></div><div class="node-item"><span>Оборудование</span><strong>'+escapeHTML(data.hardwareVersion||'—')+'</strong></div><div class="node-item"><span>ПО</span><strong>'+escapeHTML(data.softwareVersion||'—')+'</strong></div></div>';
      const alerts=data.alerts||[];
      if(alerts.length){html+='<div class="alerts"><strong>Текущие ошибки подключения</strong><ul>'+alerts.map(alert=>'<li>'+escapeHTML(alert)+'</li>').join('')+'</ul></div>'}
      const events=data.events||[];
      html+='<h2>Журнал ошибок подключения</h2>';
      if(!events.length){html+='<p class="empty">Ошибок подключения пока нет.</p>'}
      else{html+='<div class="table-wrap"><table><thead><tr><th>Время</th><th>Уровень</th><th>Сообщение</th></tr></thead><tbody>'+events.map(event=>'<tr><td>'+escapeHTML(formatTime(event.time))+'</td><td class="log-'+escapeHTML(event.level)+'">'+escapeHTML(event.level==='warning'?'предупреждение':'ошибка')+'</td><td>'+escapeHTML(event.message)+(event.count>1?' <span class="muted">×'+event.count+'</span>':'')+'</td></tr>').join('')+'</tbody></table></div>'}
      content.innerHTML=html;
      renderMetricsChart(data.history||[]);
      bindChartHover();
      const reboot=document.querySelector('#reboot-starlink');
      if(reboot){reboot.disabled=!online;if(!reboot.disabled&&reboot.textContent==='Starlink перезагружается…')reboot.textContent='Перезагрузить Starlink'}
    }
    const rebootButton=document.querySelector('#reboot-starlink');
    if(rebootButton)rebootButton.addEventListener('click',async()=>{if(!confirm('Перезагрузить терминал Starlink? Интернет временно отключится.'))return;rebootButton.disabled=true;rebootButton.textContent='Starlink перезагружается…';try{const response=await fetch('/api/system/starlink/reboot',{method:'POST',headers:{'X-DronService-Action':'reboot-starlink'}}),result=await response.json().catch(()=>({}));if(!response.ok)throw new Error(result.error||'Не удалось перезагрузить Starlink');setTimeout(load,8000)}catch(error){alert(error.message);rebootButton.disabled=false;rebootButton.textContent='Перезагрузить Starlink'}});
    content.addEventListener('click',event=>{const button=event.target.closest('[data-chart-range]');if(!button)return;chartRange=button.dataset.chartRange;localStorage.setItem('starlink-chart-range',chartRange);load()});
    async function load(){try{const response=await fetch('/api/starlink?range='+encodeURIComponent(chartRange));const data=await response.json();if(!response.ok)throw new Error(data.error||'Starlink недоступен');if(data.historyRange)chartRange=data.historyRange;render(data)}catch(error){content.innerHTML='<p class="error-box">'+escapeHTML(error.message)+'</p>'}}
    load();setInterval(load,5000);
    window.addEventListener('resize',()=>{renderMetricsChart(latestHistory);bindChartHover()});
  </script>
</main>
</div>
</body>
</html>`
