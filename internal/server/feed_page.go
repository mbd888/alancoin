package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const feedPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Feed · Alancoin</title>
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
        .container { max-width: 800px; margin: 0 auto; padding: 0 24px; }
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
        
        .feed-header {
            padding: 48px 0 24px;
            display: flex; justify-content: space-between; align-items: flex-end;
            border-bottom: 1px solid var(--border);
        }
        .feed-title { font-size: 24px; font-weight: 600; margin-bottom: 4px; }
        .feed-desc { color: var(--text-secondary); }
        .live-badge {
            display: flex; align-items: center; gap: 8px;
            background: var(--bg-subtle); border: 1px solid var(--border);
            padding: 8px 14px; border-radius: 20px; font-size: 13px; color: var(--text-secondary);
        }
        .live-dot {
            width: 8px; height: 8px; background: var(--accent); border-radius: 50%;
            animation: pulse 2s ease-in-out infinite;
        }
        @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }
        
        .tx-list { padding: 0; }
        .tx {
            display: grid; grid-template-columns: 1fr auto;
            gap: 16px; padding: 20px 0; border-bottom: 1px solid var(--border);
            align-items: start;
        }
        .tx.new { animation: slideIn 0.3s ease-out; }
        @keyframes slideIn { from { opacity: 0; transform: translateY(-8px); } to { opacity: 1; transform: translateY(0); } }
        
        .tx-main { }
        .tx-parties { display: flex; align-items: center; gap: 10px; margin-bottom: 8px; flex-wrap: wrap; }
        .tx-agent {
            background: var(--bg-subtle); padding: 6px 12px; border-radius: 6px;
            font-weight: 500; font-size: 14px;
        }
        .tx-agent a { color: inherit; text-decoration: none; }
        .tx-agent a:hover { color: var(--accent); }
        .tx-arrow { color: var(--text-tertiary); }
        .tx-service {
            color: var(--text-secondary); font-size: 13px;
            display: flex; align-items: center; gap: 8px;
        }
        .tx-service-type {
            background: var(--bg); border: 1px solid var(--border);
            padding: 2px 8px; border-radius: 4px; font-size: 11px;
            text-transform: uppercase; color: var(--text-tertiary);
        }
        .tx-right { text-align: right; }
        .tx-amount { font-size: 18px; font-weight: 600; color: var(--accent); }
        .tx-time { font-size: 12px; color: var(--text-tertiary); margin-top: 4px; }
        .tx-hash { font-size: 11px; color: var(--text-tertiary); margin-top: 4px; }
        .tx-hash a { color: var(--text-tertiary); text-decoration: none; }
        .tx-hash a:hover { color: var(--text-secondary); }
        
        .empty { text-align: center; padding: 80px 24px; color: var(--text-tertiary); }
        
        footer { border-top: 1px solid var(--border); padding: 24px 0; margin-top: 48px; text-align: center; color: var(--text-tertiary); font-size: 13px; }
        footer a { color: var(--text-secondary); text-decoration: none; margin: 0 12px; }
    </style>
</head>
<body>
    <header><div class="container header-inner">
        <a href="/" class="logo"><div class="logo-mark"></div><span class="logo-text">Alancoin</span></a>
        <nav>
            <a href="/">Dashboard</a>
            <a href="/agents">Agents</a>
            <a href="/services">Services</a>
            <a href="/feed" class="active">Feed</a>
        </nav>
    </div></header>
    <main class="container">
        <div class="feed-header">
            <div>
                <h1 class="feed-title">Transaction Feed</h1>
                <p class="feed-desc">Agents paying agents in real-time</p>
            </div>
            <div class="live-badge"><span class="live-dot"></span> Live</div>
        </div>
        <div class="tx-list" id="feed"><div class="empty">Loading transactions...</div></div>
    </main>
    <footer><div class="container"><a href="/v1/feed">API</a><a href="/">Dashboard</a><a href="https://github.com/mbd888/alancoin">GitHub</a></div></footer>
    <script>
        const formatUSD = n => { const x = parseFloat(n)||0; return x >= 1 ? '$'+x.toFixed(2) : '$'+x.toFixed(4); };
        const timeAgo = ts => {
            const diff = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
            if (diff < 5) return 'now';
            if (diff < 60) return diff + 's ago';
            if (diff < 3600) return Math.floor(diff/60) + 'm ago';
            if (diff < 86400) return Math.floor(diff/3600) + 'h ago';
            return Math.floor(diff/86400) + 'd ago';
        };
        
        function render(feed) {
            if (!feed?.length) return '<div class="empty">No transactions yet.<br>Agents will appear here when they transact.</div>';
            return feed.map(tx => 
                '<div class="tx">'+
                    '<div class="tx-main">'+
                        '<div class="tx-parties">'+
                            '<span class="tx-agent"><a href="/agent/'+tx.fromAddress+'">'+tx.fromName+'</a></span>'+
                            '<span class="tx-arrow">→</span>'+
                            '<span class="tx-agent"><a href="/agent/'+tx.toAddress+'">'+tx.toName+'</a></span>'+
                        '</div>'+
                        (tx.serviceName ? '<div class="tx-service"><span class="tx-service-type">'+tx.serviceType+'</span>'+tx.serviceName+'</div>' : '')+
                    '</div>'+
                    '<div class="tx-right">'+
                        '<div class="tx-amount mono">'+formatUSD(tx.amount)+'</div>'+
                        '<div class="tx-time">'+timeAgo(tx.timestamp)+'</div>'+
                        (tx.txHash ? '<div class="tx-hash mono"><a href="https://sepolia.basescan.org/tx/'+tx.txHash+'" target="_blank">'+tx.txHash.slice(0,10)+'...</a></div>' : '')+
                    '</div>'+
                '</div>'
            ).join('');
        }
        
        function load() {
            fetch('/v1/feed?limit=30').then(r=>r.json()).then(data => {
                document.getElementById('feed').innerHTML = render(data.feed);
            });
        }
        
        load();
        setInterval(load, 5000);
    </script>
</body>
</html>`

func feedPageHandler(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, feedPageHTML)
}
