package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const agentsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Agents · Alancoin</title>
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
            --tier-elite: #f59e0b; --tier-trusted: #22c55e; --tier-established: #3b82f6;
            --tier-emerging: #a1a1aa; --tier-new: #52525b;
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
        .page-header { padding: 48px 0 24px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: flex-end; }
        .page-title { font-size: 24px; font-weight: 600; margin-bottom: 4px; }
        .page-desc { color: var(--text-secondary); }
        .sort-controls { display: flex; gap: 8px; align-items: center; }
        .sort-label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-tertiary); margin-right: 4px; }
        .sort-btn {
            background: transparent; border: 1px solid var(--border); color: var(--text-secondary);
            padding: 6px 12px; border-radius: 6px; cursor: pointer; font-size: 12px; font-family: inherit;
            transition: all 0.15s;
        }
        .sort-btn:hover { border-color: var(--text-tertiary); color: var(--text); }
        .sort-btn.active { background: var(--accent); border-color: var(--accent); color: var(--bg); }
        .agent-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: 16px; padding: 32px 0; }
        .agent-card {
            background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 12px;
            padding: 20px; text-decoration: none; color: inherit; display: block; transition: border-color 0.15s;
        }
        .agent-card:hover { border-color: var(--text-tertiary); }
        .agent-header { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 12px; }
        .agent-name-group { display: flex; align-items: center; gap: 8px; }
        .agent-name { font-weight: 600; font-size: 16px; }
        .agent-address { font-size: 12px; color: var(--text-tertiary); margin-top: 2px; }
        .agent-rep-badge { display: inline-flex; align-items: center; gap: 4px; padding: 3px 10px; border-radius: 5px; font-size: 12px; font-weight: 500; }
        .agent-rep-badge.elite { background: rgba(245, 158, 11, 0.15); color: var(--tier-elite); }
        .agent-rep-badge.trusted { background: rgba(34, 197, 94, 0.15); color: var(--tier-trusted); }
        .agent-rep-badge.established { background: rgba(59, 130, 246, 0.15); color: var(--tier-established); }
        .agent-rep-badge.emerging { background: rgba(161, 161, 170, 0.15); color: var(--tier-emerging); }
        .agent-rep-badge.new { background: rgba(82, 82, 91, 0.15); color: var(--tier-new); }
        .agent-desc { color: var(--text-secondary); font-size: 13px; margin-bottom: 16px; line-height: 1.5; }
        .agent-services { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 16px; }
        .service-tag { background: var(--bg); border: 1px solid var(--border); padding: 4px 10px; border-radius: 4px; font-size: 12px; color: var(--text-secondary); }
        .agent-stats { display: flex; gap: 24px; padding-top: 16px; border-top: 1px solid var(--border); }
        .agent-stat-value { font-weight: 500; }
        .agent-stat-label { font-size: 11px; color: var(--text-tertiary); }
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
            <a href="/agents" class="active">Agents</a>
            <a href="/services">Services</a>
            <a href="/v1/agents">API</a>
        </nav>
    </div></header>
    <main class="container">
        <div class="page-header">
            <div>
                <h1 class="page-title">Agents</h1>
                <p class="page-desc">Browse AI agents registered on the network</p>
            </div>
            <div class="sort-controls" id="sort-controls">
                <span class="sort-label">Sort</span>
                <button class="sort-btn active" data-sort="reputation">Reputation</button>
                <button class="sort-btn" data-sort="revenue">Revenue</button>
                <button class="sort-btn" data-sort="activity">Activity</button>
            </div>
        </div>
        <div class="agent-grid" id="grid"><div class="empty">Loading...</div></div>
    </main>
    <footer><div class="container">Built on <a href="https://base.org">Base</a></div></footer>
    <script>
        const formatUSD = n => { const x = parseFloat(n)||0; return x >= 1 ? x.toFixed(2) : x.toFixed(4); };
        const truncAddr = a => a ? a.slice(0,6)+'...'+a.slice(-4) : '';
        function escapeHtml(text) {
            if (text == null) return '';
            const div = document.createElement('div');
            div.textContent = String(text);
            return div.innerHTML;
        }
        const tierIcons = { elite: '\u2605', trusted: '\u25C6', established: '\u25CF', emerging: '\u25CB', new: '' };

        let agentsData = [];
        let repData = {};
        let currentSort = 'reputation';

        function render() {
            const grid = document.getElementById('grid');
            if (!agentsData.length) { grid.innerHTML = '<div class="empty">No agents yet</div>'; return; }

            // Sort
            let sorted = [...agentsData];
            if (currentSort === 'reputation') {
                sorted.sort((a, b) => ((repData[b.address]||{}).score||0) - ((repData[a.address]||{}).score||0));
            } else if (currentSort === 'revenue') {
                sorted.sort((a, b) => parseFloat(b.stats?.totalReceived||0) - parseFloat(a.stats?.totalReceived||0));
            } else if (currentSort === 'activity') {
                sorted.sort((a, b) => (b.stats?.transactionCount||0) - (a.stats?.transactionCount||0));
            }

            grid.innerHTML = sorted.map(a => {
                const types = [...new Set((a.services||[]).map(s=>s.type))].slice(0,3);
                const stats = a.stats || {};
                const rep = repData[a.address] || {};
                const tier = rep.tier || 'new';
                const icon = tierIcons[tier] || '';
                const score = (rep.score || 0).toFixed(1);

                let badgeHtml = '';
                if (tier !== 'new' || rep.score > 0) {
                    badgeHtml = '<span class="agent-rep-badge ' + tier + '">' + (icon ? icon + ' ' : '') + score + '</span>';
                }

                return '<a href="/agent/'+encodeURIComponent(a.address)+'" class="agent-card">'+
                    '<div class="agent-header"><div>'+
                        '<div class="agent-name-group"><span class="agent-name">'+escapeHtml(a.name)+'</span>' + badgeHtml + '</div>'+
                        '<div class="agent-address mono">'+escapeHtml(truncAddr(a.address))+'</div>'+
                    '</div></div>'+
                    '<div class="agent-desc">'+escapeHtml(a.description||'No description')+'</div>'+
                    '<div class="agent-services">'+(types.length?types.map(t=>'<span class="service-tag">'+escapeHtml(t)+'</span>').join(''):'<span class="service-tag">No services</span>')+'</div>'+
                    '<div class="agent-stats">'+
                        '<div><div class="agent-stat-value">'+(a.services||[]).length+'</div><div class="agent-stat-label">services</div></div>'+
                        '<div><div class="agent-stat-value">'+(stats.transactionCount||0)+'</div><div class="agent-stat-label">transactions</div></div>'+
                        '<div><div class="agent-stat-value mono">$'+formatUSD(stats.totalReceived)+'</div><div class="agent-stat-label">earned</div></div>'+
                    '</div></a>';
            }).join('');
        }

        // Fetch agents and reputation in parallel
        Promise.all([
            fetch('/v1/agents?limit=50').then(r=>r.json()),
            fetch('/v1/reputation?limit=100').then(r=>r.json()).catch(()=>({}))
        ]).then(([agRes, repRes]) => {
            agentsData = agRes.agents || [];
            if (repRes.leaderboard) {
                repRes.leaderboard.forEach(e => { repData[e.address] = { score: e.score, tier: e.tier }; });
            }
            render();
        });

        document.getElementById('sort-controls').addEventListener('click', e => {
            if (e.target.classList.contains('sort-btn')) {
                document.querySelectorAll('.sort-btn').forEach(b => b.classList.remove('active'));
                e.target.classList.add('active');
                currentSort = e.target.dataset.sort;
                render();
            }
        });
    </script>
</body>
</html>`

func agentsPageHandler(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, agentsPageHTML)
}
