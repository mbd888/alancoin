package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const agentProfileHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Agent · Alancoin</title>
    <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>◉</text></svg>">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root {
            --bg: #09090b; --bg-subtle: #18181b; --border: #27272a;
            --text: #fafafa; --text-secondary: #a1a1aa; --text-tertiary: #52525b;
            --accent: #22c55e; --red: #ef4444;
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
        .container { max-width: 900px; margin: 0 auto; padding: 0 24px; }
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
        nav a:hover { color: var(--text); }

        .profile { padding: 48px 0; }
        .profile-header { display: flex; gap: 24px; margin-bottom: 32px; }
        .profile-avatar {
            width: 80px; height: 80px; background: linear-gradient(135deg, var(--accent), #3b82f6);
            border-radius: 16px; display: flex; align-items: center; justify-content: center;
            font-size: 2rem; flex-shrink: 0;
        }
        .profile-info { flex: 1; }
        .profile-name { font-size: 28px; font-weight: 600; margin-bottom: 4px; }
        .profile-address { color: var(--text-tertiary); margin-bottom: 12px; word-break: break-all; }
        .profile-address a { color: var(--text-tertiary); text-decoration: none; }
        .profile-address a:hover { color: var(--text-secondary); }
        .profile-desc { color: var(--text-secondary); line-height: 1.6; }

        .stats-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; margin-bottom: 32px; }
        .stat-card { background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 12px; padding: 20px; text-align: center; }
        .stat-value { font-size: 24px; font-weight: 600; }
        .stat-value.accent { color: var(--accent); }
        .stat-label { font-size: 12px; color: var(--text-tertiary); margin-top: 4px; text-transform: uppercase; letter-spacing: 0.05em; }

        /* Reputation card */
        .rep-card {
            background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 12px;
            padding: 24px; margin-bottom: 48px;
        }
        .rep-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
        .rep-score-block { display: flex; align-items: baseline; gap: 12px; }
        .rep-score-num { font-size: 36px; font-weight: 600; }
        .rep-tier-badge { display: inline-flex; align-items: center; gap: 6px; padding: 4px 12px; border-radius: 6px; font-size: 13px; font-weight: 500; }
        .rep-tier-badge.elite { background: rgba(245, 158, 11, 0.15); color: var(--tier-elite); }
        .rep-tier-badge.trusted { background: rgba(34, 197, 94, 0.15); color: var(--tier-trusted); }
        .rep-tier-badge.established { background: rgba(59, 130, 246, 0.15); color: var(--tier-established); }
        .rep-tier-badge.emerging { background: rgba(161, 161, 170, 0.15); color: var(--tier-emerging); }
        .rep-tier-badge.new { background: rgba(82, 82, 91, 0.15); color: var(--tier-new); }
        .rep-metrics { display: flex; gap: 24px; margin-bottom: 20px; font-size: 13px; color: var(--text-secondary); }
        .rep-metric-val { font-weight: 500; color: var(--text); }
        .rep-components { display: flex; flex-direction: column; gap: 10px; }
        .rep-comp-row { display: flex; align-items: center; gap: 12px; }
        .rep-comp-label { width: 80px; font-size: 12px; color: var(--text-secondary); flex-shrink: 0; }
        .rep-comp-bar { flex: 1; height: 8px; background: var(--bg); border-radius: 4px; overflow: hidden; }
        .rep-comp-fill { height: 100%; border-radius: 4px; transition: width 0.5s; }
        .rep-comp-val { width: 36px; font-size: 12px; color: var(--text-tertiary); text-align: right; flex-shrink: 0; }

        .section { margin-bottom: 48px; }
        .section-title { font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-tertiary); margin-bottom: 16px; }

        .service-card {
            background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 10px;
            padding: 16px 20px; margin-bottom: 8px;
            display: flex; justify-content: space-between; align-items: center;
        }
        .service-left { display: flex; align-items: center; gap: 12px; }
        .service-type { font-size: 11px; text-transform: uppercase; color: var(--text-tertiary); background: var(--bg); padding: 4px 8px; border-radius: 4px; }
        .service-name { font-weight: 500; }
        .service-price { font-weight: 500; }

        .tx-row {
            display: flex; justify-content: space-between; align-items: center;
            padding: 14px 0; border-bottom: 1px solid var(--border);
        }
        .tx-row:last-child { border-bottom: none; }
        .tx-parties { display: flex; align-items: center; gap: 8px; }
        .tx-agent { background: var(--bg-subtle); padding: 4px 10px; border-radius: 6px; font-size: 13px; }
        .tx-agent.self { background: rgba(34, 197, 94, 0.15); color: var(--accent); }
        .tx-arrow { color: var(--text-tertiary); }
        .tx-meta { text-align: right; }
        .tx-amount { font-weight: 500; }
        .tx-amount.in { color: var(--accent); }
        .tx-amount.out { color: var(--red); }
        .tx-time { font-size: 12px; color: var(--text-tertiary); }

        .empty { text-align: center; padding: 40px; color: var(--text-tertiary); background: var(--bg-subtle); border-radius: 12px; }
        .loading { text-align: center; padding: 80px; color: var(--text-tertiary); }
        .error { text-align: center; padding: 80px; }
        .error h2 { margin-bottom: 8px; }
        .error a { color: var(--accent); }

        footer { border-top: 1px solid var(--border); padding: 24px 0; margin-top: 48px; text-align: center; color: var(--text-tertiary); font-size: 13px; }
        footer a { color: var(--text-secondary); text-decoration: none; }

        @media (max-width: 600px) {
            .stats-grid { grid-template-columns: repeat(2, 1fr); }
            .profile-header { flex-direction: column; align-items: center; text-align: center; }
        }
    </style>
</head>
<body>
    <header><div class="container header-inner">
        <a href="/" class="logo"><div class="logo-mark"></div><span class="logo-text">Alancoin</span></a>
        <nav>
            <a href="/">Dashboard</a>
            <a href="/agents">Agents</a>
            <a href="/services">Services</a>
        </nav>
    </div></header>
    <main class="container">
        <div id="content"><div class="loading">Loading...</div></div>
    </main>
    <footer><div class="container">Built on <a href="https://base.org">Base</a></div></footer>
    <script>
        const addr = location.pathname.split('/agent/')[1];
        const formatUSD = n => { const x = parseFloat(n)||0; return x >= 1 ? '$'+x.toFixed(2) : '$'+x.toFixed(4); };
        const truncAddr = a => a ? a.slice(0,6)+'...'+a.slice(-4) : '';
        function escapeHtml(text) {
            if (text == null) return '';
            const div = document.createElement('div');
            div.textContent = String(text);
            return div.innerHTML;
        }
        const tierIcons = { elite: '\u2605', trusted: '\u25C6', established: '\u25CF', emerging: '\u25CB', new: '' };
        const compColors = {
            volumeScore: '#8b5cf6',
            activityScore: '#3b82f6',
            successScore: '#22c55e',
            ageScore: '#f59e0b',
            diversityScore: '#ec4899'
        };
        const compLabels = {
            volumeScore: 'Volume',
            activityScore: 'Activity',
            successScore: 'Success',
            ageScore: 'Age',
            diversityScore: 'Diversity'
        };

        Promise.all([
            fetch('/v1/agents/' + addr).then(r => { if (!r.ok) throw new Error('Not found'); return r.json(); }),
            fetch('/v1/agents/' + addr + '/transactions?limit=15').then(r => r.json()).catch(() => ({ transactions: [] })),
            fetch('/v1/reputation/' + addr).then(r => r.ok ? r.json() : null).catch(() => null)
        ]).then(([agent, txData, repData]) => {
            const stats = agent.stats || {};
            const services = agent.services || [];
            const txs = txData.transactions || [];
            const rep = repData?.reputation || null;

            document.title = agent.name + ' \u00B7 Alancoin';

            // Reputation card HTML
            let repCardHtml = '';
            if (rep) {
                const tier = rep.tier || 'new';
                const icon = tierIcons[tier] || '';
                const score = (rep.score || 0).toFixed(1);
                const metrics = rep.metrics || {};
                const components = rep.components || {};

                repCardHtml = '<div class="rep-card">' +
                    '<div class="rep-header">' +
                        '<div class="rep-score-block">' +
                            '<span class="rep-score-num mono">' + score + '</span>' +
                            '<span class="rep-tier-badge ' + tier + '">' + (icon ? icon + ' ' : '') + tier.charAt(0).toUpperCase() + tier.slice(1) + '</span>' +
                        '</div>' +
                    '</div>' +
                    '<div class="rep-metrics">' +
                        '<div><span class="rep-metric-val">' + (metrics.totalTransactions || 0) + '</span> transactions</div>' +
                        '<div><span class="rep-metric-val">' + (metrics.uniqueCounterparties || 0) + '</span> counterparties</div>' +
                        '<div><span class="rep-metric-val">' + (metrics.daysOnNetwork || 0) + '</span> days</div>' +
                    '</div>' +
                    '<div class="rep-components">';

                ['volumeScore', 'activityScore', 'successScore', 'ageScore', 'diversityScore'].forEach(key => {
                    const val = (components[key] || 0).toFixed(0);
                    const color = compColors[key];
                    const label = compLabels[key];
                    repCardHtml += '<div class="rep-comp-row">' +
                        '<span class="rep-comp-label">' + label + '</span>' +
                        '<div class="rep-comp-bar"><div class="rep-comp-fill" style="width:' + val + '%;background:' + color + '"></div></div>' +
                        '<span class="rep-comp-val mono">' + val + '</span>' +
                    '</div>';
                });

                repCardHtml += '</div></div>';
            }

            let html = '<div class="profile">'+
                '<div class="profile-header">'+
                    '<div class="profile-avatar">\uD83E\uDD16</div>'+
                    '<div class="profile-info">'+
                        '<h1 class="profile-name">'+escapeHtml(agent.name)+'</h1>'+
                        '<div class="profile-address mono"><a href="https://sepolia.basescan.org/address/'+encodeURIComponent(agent.address)+'" target="_blank">'+escapeHtml(agent.address)+'</a></div>'+
                        '<div class="profile-desc">'+escapeHtml(agent.description||'No description')+'</div>'+
                    '</div>'+
                '</div>'+

                '<div class="stats-grid">'+
                    '<div class="stat-card"><div class="stat-value">'+services.length+'</div><div class="stat-label">Services</div></div>'+
                    '<div class="stat-card"><div class="stat-value">'+(stats.transactionCount||0)+'</div><div class="stat-label">Transactions</div></div>'+
                    '<div class="stat-card"><div class="stat-value accent mono">'+formatUSD(stats.totalReceived)+'</div><div class="stat-label">Earned</div></div>'+
                    '<div class="stat-card"><div class="stat-value">' + (rep ? rep.score.toFixed(1) : '-') + '</div><div class="stat-label">Reputation</div></div>'+
                '</div>'+

                repCardHtml+

                '<div class="section">'+
                    '<div class="section-title">Services</div>'+
                    (services.length ? services.map(s =>
                        '<div class="service-card">'+
                            '<div class="service-left">'+
                                '<span class="service-type">'+escapeHtml(s.type)+'</span>'+
                                '<span class="service-name">'+escapeHtml(s.name)+'</span>'+
                            '</div>'+
                            '<span class="service-price mono">'+formatUSD(s.price)+'</span>'+
                        '</div>'
                    ).join('') : '<div class="empty">No services</div>')+
                '</div>'+

                '<div class="section">'+
                    '<div class="section-title">Recent Transactions</div>'+
                    (txs.length ? txs.map(tx => {
                        const isOut = tx.from.toLowerCase() === agent.address.toLowerCase();
                        return '<div class="tx-row">'+
                            '<div class="tx-parties">'+
                                '<span class="tx-agent'+(isOut?' self':'')+'">'+escapeHtml(!isOut?truncAddr(tx.from):agent.name)+'</span>'+
                                '<span class="tx-arrow">\u2192</span>'+
                                '<span class="tx-agent'+(!isOut?' self':'')+'">'+escapeHtml(isOut?truncAddr(tx.to):agent.name)+'</span>'+
                            '</div>'+
                            '<div class="tx-meta">'+
                                '<div class="tx-amount '+(isOut?'out':'in')+' mono">'+(isOut?'-':'+')+formatUSD(tx.amount)+'</div>'+
                                '<div class="tx-time">'+(tx.timeAgo||'recently')+'</div>'+
                            '</div>'+
                        '</div>';
                    }).join('') : '<div class="empty">No transactions</div>')+
                '</div>'+
            '</div>';

            document.getElementById('content').innerHTML = html;
        }).catch(err => {
            document.getElementById('content').innerHTML = '<div class="error"><h2>Agent Not Found</h2><p><a href="/agents">\u2190 Back to agents</a></p></div>';
        });
    </script>
</body>
</html>`

func agentProfileHandler(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, agentProfileHTML)
}
