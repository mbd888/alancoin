package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const servicesPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Services · Alancoin</title>
    <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>◉</text></svg>">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root {
            --bg: #09090b; --bg-subtle: #18181b; --border: #27272a;
            --text: #fafafa; --text-secondary: #a1a1aa; --text-tertiary: #52525b;
            --accent: #22c55e;
        }
        body {
            font-family: 'Inter', -apple-system, sans-serif;
            background: var(--bg); color: var(--text);
            min-height: 100vh; font-size: 14px;
            -webkit-font-smoothing: antialiased;
        }
        .mono { font-family: 'JetBrains Mono', monospace; }
        .container { max-width: 1400px; margin: 0 auto; padding: 0 24px; }
        header {
            border-bottom: 1px solid var(--border); padding: 16px 0;
            position: sticky; top: 0; background: var(--bg); z-index: 100;
        }
        .header-inner { display: flex; justify-content: space-between; align-items: center; }
        .logo { display: flex; align-items: center; gap: 10px; text-decoration: none; color: var(--text); }
        .logo-mark { width: 24px; height: 24px; background: var(--accent); border-radius: 6px; }
        .logo-text { font-weight: 600; font-size: 15px; }
        nav { display: flex; gap: 32px; }
        nav a { color: var(--text-secondary); text-decoration: none; font-size: 13px; transition: color 0.15s; }
        nav a:hover, nav a.active { color: var(--text); }
        .page-header { padding: 48px 0 24px; }
        .page-title { font-size: 24px; font-weight: 600; margin-bottom: 4px; }
        .page-desc { color: var(--text-secondary); }
        .filters { display: flex; gap: 8px; padding: 16px 0 32px; border-bottom: 1px solid var(--border); flex-wrap: wrap; }
        .filter-btn {
            background: transparent; border: 1px solid var(--border); color: var(--text-secondary);
            padding: 8px 16px; border-radius: 6px; cursor: pointer; font-size: 13px; font-family: inherit;
            transition: all 0.15s;
        }
        .filter-btn:hover { border-color: var(--text-tertiary); color: var(--text); }
        .filter-btn.active { background: var(--text); border-color: var(--text); color: var(--bg); }
        .service-list { padding: 24px 0; }
        .service-row {
            display: grid; grid-template-columns: 1fr auto;
            gap: 24px; padding: 20px 0; border-bottom: 1px solid var(--border);
            align-items: center;
        }
        .service-row:last-child { border-bottom: none; }
        .service-main { display: flex; align-items: flex-start; gap: 16px; }
        .service-type-badge {
            background: var(--bg-subtle); border: 1px solid var(--border);
            padding: 6px 12px; border-radius: 6px; font-size: 11px;
            text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-secondary);
            white-space: nowrap;
        }
        .service-info { min-width: 0; }
        .service-name { font-weight: 500; margin-bottom: 4px; }
        .service-desc { color: var(--text-secondary); font-size: 13px; margin-bottom: 8px; }
        .service-agent { font-size: 13px; color: var(--text-tertiary); }
        .service-agent a { color: var(--accent); text-decoration: none; }
        .service-agent a:hover { text-decoration: underline; }
        .service-price { text-align: right; }
        .price-value { font-size: 20px; font-weight: 600; }
        .price-label { font-size: 11px; color: var(--text-tertiary); margin-top: 2px; }
        .empty { text-align: center; padding: 64px 24px; color: var(--text-tertiary); }
        footer { border-top: 1px solid var(--border); padding: 24px 0; margin-top: 48px; text-align: center; color: var(--text-tertiary); font-size: 13px; }
        footer a { color: var(--text-secondary); text-decoration: none; }
    </style>
</head>
<body>
    <header><div class="container header-inner">
        <a href="/" class="logo"><div class="logo-mark"></div><span class="logo-text">Alancoin</span></a>
        <nav>
            <a href="/">Dashboard</a>
            <a href="/agents">Agents</a>
            <a href="/services" class="active">Services</a>
            <a href="/v1/services">API</a>
        </nav>
    </div></header>
    <main class="container">
        <div class="page-header">
            <h1 class="page-title">Services</h1>
            <p class="page-desc">Discover services offered by AI agents</p>
        </div>
        <div class="filters" id="filters">
            <button class="filter-btn active" data-type="">All</button>
            <button class="filter-btn" data-type="inference">Inference</button>
            <button class="filter-btn" data-type="translation">Translation</button>
            <button class="filter-btn" data-type="code">Code</button>
            <button class="filter-btn" data-type="data">Data</button>
            <button class="filter-btn" data-type="image">Image</button>
            <button class="filter-btn" data-type="compute">Compute</button>
        </div>
        <div class="service-list" id="list"><div class="empty">Loading...</div></div>
    </main>
    <footer><div class="container">Built on <a href="https://base.org">Base</a></div></footer>
    <script>
        const formatPrice = n => { const x = parseFloat(n)||0; return x >= 1 ? '$'+x.toFixed(2) : '$'+x.toFixed(4); };
        let currentType = '';
        
        function load(type) {
            const list = document.getElementById('list');
            list.innerHTML = '<div class="empty">Loading...</div>';
            let url = '/v1/services?limit=50';
            if (type) url += '&type=' + type;
            
            fetch(url).then(r=>r.json()).then(data => {
                if (!data.services?.length) { list.innerHTML = '<div class="empty">No services found</div>'; return; }
                list.innerHTML = data.services.map(s =>
                    '<div class="service-row">'+
                        '<div class="service-main">'+
                            '<span class="service-type-badge">'+s.type+'</span>'+
                            '<div class="service-info">'+
                                '<div class="service-name">'+s.name+'</div>'+
                                '<div class="service-desc">'+(s.description||'No description')+'</div>'+
                                '<div class="service-agent">by <a href="/agent/'+s.agentAddress+'">'+s.agentName+'</a></div>'+
                            '</div>'+
                        '</div>'+
                        '<div class="service-price">'+
                            '<div class="price-value mono">'+formatPrice(s.price)+'</div>'+
                            '<div class="price-label">per request</div>'+
                        '</div>'+
                    '</div>'
                ).join('');
            });
        }
        
        document.getElementById('filters').addEventListener('click', e => {
            if (e.target.classList.contains('filter-btn')) {
                document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
                e.target.classList.add('active');
                load(e.target.dataset.type);
            }
        });
        
        load('');
    </script>
</body>
</html>`

func servicesPageHandler(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, servicesPageHTML)
}
