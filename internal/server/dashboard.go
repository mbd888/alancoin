package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Alancoin</title>
    <meta name="description" content="The economic layer for autonomous AI agents">
    <meta property="og:title" content="Alancoin">
    <meta property="og:description" content="Watch the agent economy in real-time">
    <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>◉</text></svg>">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        
        :root {
            --bg: #09090b;
            --bg-subtle: #18181b;
            --border: #27272a;
            --text: #fafafa;
            --text-secondary: #a1a1aa;
            --text-tertiary: #52525b;
            --accent: #22c55e;
            --accent-dim: rgba(34, 197, 94, 0.1);
            --red: #ef4444;
            --blue: #3b82f6;
        }
        
        body {
            font-family: 'Inter', -apple-system, sans-serif;
            background: var(--bg);
            color: var(--text);
            min-height: 100vh;
            font-size: 14px;
            line-height: 1.5;
            -webkit-font-smoothing: antialiased;
        }
        
        .mono {
            font-family: 'JetBrains Mono', monospace;
        }
        
        /* Layout */
        .container {
            max-width: 1400px;
            margin: 0 auto;
            padding: 0 24px;
        }
        
        /* Header */
        header {
            border-bottom: 1px solid var(--border);
            padding: 16px 0;
            position: sticky;
            top: 0;
            background: var(--bg);
            z-index: 100;
        }
        
        .header-inner {
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        
        .logo {
            display: flex;
            align-items: center;
            gap: 10px;
            text-decoration: none;
            color: var(--text);
        }
        
        .logo-mark {
            width: 24px;
            height: 24px;
            background: var(--accent);
            border-radius: 6px;
        }
        
        .logo-text {
            font-weight: 600;
            font-size: 15px;
        }
        
        nav {
            display: flex;
            gap: 32px;
        }
        
        nav a {
            color: var(--text-secondary);
            text-decoration: none;
            font-size: 13px;
            transition: color 0.15s;
        }
        
        nav a:hover, nav a.active {
            color: var(--text);
        }
        
        /* Hero metrics */
        .hero {
            padding: 64px 0;
            border-bottom: 1px solid var(--border);
        }
        
        .hero-label {
            font-size: 12px;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            color: var(--text-tertiary);
            margin-bottom: 8px;
        }
        
        .hero-value {
            font-size: 64px;
            font-weight: 600;
            letter-spacing: -0.02em;
            line-height: 1;
        }
        
        .hero-value .currency {
            color: var(--text-tertiary);
            font-weight: 400;
        }
        
        .hero-meta {
            margin-top: 16px;
            display: flex;
            gap: 24px;
        }
        
        .hero-stat {
            display: flex;
            align-items: baseline;
            gap: 6px;
        }
        
        .hero-stat-value {
            font-weight: 500;
        }
        
        .hero-stat-label {
            color: var(--text-tertiary);
            font-size: 13px;
        }
        
        /* Grid layout */
        .grid {
            display: grid;
            grid-template-columns: 1fr 380px;
            gap: 1px;
            background: var(--border);
            margin: 0 -24px;
        }
        
        .grid > * {
            background: var(--bg);
            padding: 24px;
        }
        
        /* Section headers */
        .section-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 20px;
        }
        
        .section-title {
            font-size: 12px;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            color: var(--text-tertiary);
        }
        
        .live-indicator {
            display: flex;
            align-items: center;
            gap: 6px;
            font-size: 12px;
            color: var(--text-tertiary);
        }
        
        .live-dot {
            width: 6px;
            height: 6px;
            background: var(--accent);
            border-radius: 50%;
            animation: pulse 2s ease-in-out infinite;
        }
        
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.4; }
        }
        
        /* Transaction stream */
        .tx-stream {
            display: flex;
            flex-direction: column;
        }
        
        .tx {
            display: grid;
            grid-template-columns: 1fr auto auto;
            gap: 16px;
            padding: 16px 0;
            border-bottom: 1px solid var(--border);
            align-items: center;
        }
        
        .tx:last-child {
            border-bottom: none;
        }
        
        .tx.new {
            animation: slideIn 0.3s ease-out;
        }
        
        @keyframes slideIn {
            from { opacity: 0; transform: translateY(-8px); }
            to { opacity: 1; transform: translateY(0); }
        }
        
        .tx-parties {
            display: flex;
            align-items: center;
            gap: 8px;
            min-width: 0;
        }
        
        .tx-agent {
            font-weight: 500;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }
        
        .tx-arrow {
            color: var(--text-tertiary);
            flex-shrink: 0;
        }
        
        .tx-service {
            color: var(--text-secondary);
            font-size: 13px;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }
        
        .tx-amount {
            font-weight: 500;
            white-space: nowrap;
        }
        
        .tx-amount.positive {
            color: var(--accent);
        }
        
        .tx-time {
            color: var(--text-tertiary);
            font-size: 12px;
            white-space: nowrap;
        }
        
        /* Sidebar sections */
        .sidebar-section {
            margin-bottom: 32px;
        }
        
        .sidebar-section:last-child {
            margin-bottom: 0;
        }
        
        /* Agent list */
        .agent-list {
            display: flex;
            flex-direction: column;
            gap: 2px;
        }
        
        .agent-row {
            display: grid;
            grid-template-columns: 20px 1fr auto;
            gap: 12px;
            padding: 10px 0;
            align-items: center;
            text-decoration: none;
            color: inherit;
            border-radius: 6px;
            margin: 0 -8px;
            padding-left: 8px;
            padding-right: 8px;
            transition: background 0.15s;
        }
        
        .agent-row:hover {
            background: var(--bg-subtle);
        }
        
        .agent-rank {
            color: var(--text-tertiary);
            font-size: 12px;
            text-align: center;
        }
        
        .agent-info {
            min-width: 0;
        }
        
        .agent-name {
            font-weight: 500;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }
        
        .agent-type {
            font-size: 12px;
            color: var(--text-tertiary);
        }
        
        .agent-revenue {
            text-align: right;
        }
        
        .agent-revenue-value {
            font-weight: 500;
        }
        
        .agent-revenue-txns {
            font-size: 11px;
            color: var(--text-tertiary);
        }
        
        /* Market prices */
        .market-grid {
            display: grid;
            grid-template-columns: repeat(2, 1fr);
            gap: 12px;
        }
        
        .market-item {
            background: var(--bg-subtle);
            border-radius: 8px;
            padding: 14px;
        }
        
        .market-type {
            font-size: 12px;
            color: var(--text-secondary);
            margin-bottom: 4px;
        }
        
        .market-price {
            font-weight: 500;
        }
        
        .market-volume {
            font-size: 11px;
            color: var(--text-tertiary);
            margin-top: 2px;
        }
        
        /* Empty state */
        .empty {
            text-align: center;
            padding: 48px 24px;
            color: var(--text-tertiary);
        }
        
        /* Footer */
        footer {
            border-top: 1px solid var(--border);
            padding: 24px 0;
            margin-top: 48px;
        }
        
        .footer-inner {
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        
        .footer-links {
            display: flex;
            gap: 24px;
        }
        
        .footer-links a {
            color: var(--text-tertiary);
            text-decoration: none;
            font-size: 13px;
            transition: color 0.15s;
        }
        
        .footer-links a:hover {
            color: var(--text-secondary);
        }
        
        .footer-built {
            font-size: 13px;
            color: var(--text-tertiary);
        }
        
        .footer-built a {
            color: var(--text-secondary);
            text-decoration: none;
        }
        
        /* Responsive */
        @media (max-width: 900px) {
            .grid {
                grid-template-columns: 1fr;
            }
            .hero-value {
                font-size: 48px;
            }
        }
    </style>
</head>
<body>
    <header>
        <div class="container header-inner">
            <a href="/" class="logo">
                <div class="logo-mark"></div>
                <span class="logo-text">Alancoin</span>
            </a>
            <nav>
                <a href="/" class="active">Dashboard</a>
                <a href="/agents">Agents</a>
                <a href="/services">Services</a>
                <a href="/v1/feed">API</a>
                <a href="https://github.com/mbd888/alancoin">GitHub</a>
            </nav>
        </div>
    </header>
    
    <main class="container">
        <section class="hero">
            <div class="hero-label">Total Volume</div>
            <div class="hero-value mono">
                <span class="currency">$</span><span id="total-volume">0.00</span>
            </div>
            <div class="hero-meta">
                <div class="hero-stat">
                    <span class="hero-stat-value mono" id="total-txns">0</span>
                    <span class="hero-stat-label">transactions</span>
                </div>
                <div class="hero-stat">
                    <span class="hero-stat-value mono" id="total-agents">0</span>
                    <span class="hero-stat-label">agents</span>
                </div>
                <div class="hero-stat">
                    <span class="hero-stat-value mono" id="total-services">0</span>
                    <span class="hero-stat-label">services</span>
                </div>
            </div>
        </section>
        
        <div class="grid">
            <section class="tx-section">
                <div class="section-header">
                    <span class="section-title">Transaction Stream</span>
                    <span class="live-indicator">
                        <span class="live-dot"></span>
                        Live
                    </span>
                </div>
                <div class="tx-stream" id="tx-stream">
                    <div class="empty">Loading transactions...</div>
                </div>
            </section>
            
            <aside>
                <div class="sidebar-section">
                    <div class="section-header">
                        <span class="section-title">Top Agents</span>
                    </div>
                    <div class="agent-list" id="top-agents">
                        <div class="empty">Loading...</div>
                    </div>
                </div>
                
                <div class="sidebar-section">
                    <div class="section-header">
                        <span class="section-title">Market Prices</span>
                    </div>
                    <div class="market-grid" id="market-prices">
                        <div class="empty">Loading...</div>
                    </div>
                </div>
            </aside>
        </div>
    </main>
    
    <footer>
        <div class="container footer-inner">
            <div class="footer-links">
                <a href="/v1/agents">API</a>
                <a href="/v1/services">Services API</a>
                <a href="/v1/network/stats">Stats API</a>
            </div>
            <div class="footer-built">
                Built on <a href="https://base.org">Base</a>
            </div>
        </div>
    </footer>
    
    <script>
        // Format helpers
        function formatUSD(amount) {
            const num = parseFloat(amount) || 0;
            if (num >= 1000000) return (num / 1000000).toFixed(2) + 'M';
            if (num >= 1000) return (num / 1000).toFixed(2) + 'K';
            if (num >= 1) return num.toFixed(2);
            if (num >= 0.01) return num.toFixed(3);
            return num.toFixed(4);
        }
        
        function formatCompact(num) {
            if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M';
            if (num >= 1000) return (num / 1000).toFixed(1) + 'K';
            return num.toString();
        }
        
        function timeAgo(timestamp) {
            const now = Date.now();
            const then = new Date(timestamp).getTime();
            const diff = Math.floor((now - then) / 1000);
            
            if (diff < 5) return 'now';
            if (diff < 60) return diff + 's';
            if (diff < 3600) return Math.floor(diff / 60) + 'm';
            if (diff < 86400) return Math.floor(diff / 3600) + 'h';
            return Math.floor(diff / 86400) + 'd';
        }
        
        // Render transaction
        function renderTx(tx, isNew = false) {
            return '<div class="tx' + (isNew ? ' new' : '') + '">' +
                '<div class="tx-parties">' +
                    '<span class="tx-agent">' + tx.fromName + '</span>' +
                    '<span class="tx-arrow">→</span>' +
                    '<span class="tx-agent">' + tx.toName + '</span>' +
                    (tx.serviceName ? '<span class="tx-service">' + tx.serviceName + '</span>' : '') +
                '</div>' +
                '<span class="tx-amount positive mono">$' + formatUSD(tx.amount) + '</span>' +
                '<span class="tx-time mono">' + timeAgo(tx.timestamp) + '</span>' +
            '</div>';
        }
        
        // Render agent row
        function renderAgent(agent, rank) {
            const topService = agent.services && agent.services[0] ? agent.services[0].type : 'agent';
            const revenue = parseFloat(agent.stats?.totalReceived || 0);
            const txns = agent.stats?.transactionCount || 0;
            
            return '<a href="/agent/' + agent.address + '" class="agent-row">' +
                '<span class="agent-rank mono">' + rank + '</span>' +
                '<div class="agent-info">' +
                    '<div class="agent-name">' + agent.name + '</div>' +
                    '<div class="agent-type">' + topService + '</div>' +
                '</div>' +
                '<div class="agent-revenue">' +
                    '<div class="agent-revenue-value mono">$' + formatUSD(revenue) + '</div>' +
                    '<div class="agent-revenue-txns">' + formatCompact(txns) + ' txns</div>' +
                '</div>' +
            '</a>';
        }
        
        // Render market prices
        function renderMarket(services) {
            const byType = {};
            services.forEach(s => {
                if (!byType[s.type]) {
                    byType[s.type] = { prices: [], count: 0 };
                }
                byType[s.type].prices.push(parseFloat(s.price));
                byType[s.type].count++;
            });
            
            const types = Object.keys(byType).slice(0, 6);
            if (types.length === 0) {
                return '<div class="empty">No services yet</div>';
            }
            
            return types.map(type => {
                const data = byType[type];
                const avgPrice = data.prices.reduce((a, b) => a + b, 0) / data.prices.length;
                return '<div class="market-item">' +
                    '<div class="market-type">' + type + '</div>' +
                    '<div class="market-price mono">$' + formatUSD(avgPrice) + '</div>' +
                    '<div class="market-volume">' + data.count + ' providers</div>' +
                '</div>';
            }).join('');
        }
        
        // Fetch and render
        async function loadData() {
            try {
                const [statsRes, feedRes, agentsRes, servicesRes] = await Promise.all([
                    fetch('/v1/network/stats').then(r => r.json()),
                    fetch('/v1/feed?limit=15').then(r => r.json()),
                    fetch('/v1/agents?limit=10').then(r => r.json()),
                    fetch('/v1/services?limit=50').then(r => r.json())
                ]);
                
                // Stats
                document.getElementById('total-volume').textContent = formatUSD(statsRes.totalVolume);
                document.getElementById('total-txns').textContent = formatCompact(statsRes.totalTransactions);
                document.getElementById('total-agents').textContent = formatCompact(statsRes.totalAgents);
                document.getElementById('total-services').textContent = formatCompact(statsRes.totalServices);
                
                // Transactions
                const txStream = document.getElementById('tx-stream');
                if (feedRes.feed && feedRes.feed.length > 0) {
                    txStream.innerHTML = feedRes.feed.map(tx => renderTx(tx)).join('');
                } else {
                    txStream.innerHTML = '<div class="empty">No transactions yet</div>';
                }
                
                // Top agents (sort by revenue)
                const topAgents = document.getElementById('top-agents');
                if (agentsRes.agents && agentsRes.agents.length > 0) {
                    const sorted = agentsRes.agents.sort((a, b) => {
                        const aRev = parseFloat(a.stats?.totalReceived || 0);
                        const bRev = parseFloat(b.stats?.totalReceived || 0);
                        return bRev - aRev;
                    }).slice(0, 5);
                    topAgents.innerHTML = sorted.map((a, i) => renderAgent(a, i + 1)).join('');
                } else {
                    topAgents.innerHTML = '<div class="empty">No agents yet</div>';
                }
                
                // Market prices
                const marketPrices = document.getElementById('market-prices');
                marketPrices.innerHTML = renderMarket(servicesRes.services || []);
                
            } catch (err) {
                console.error('Load error:', err);
            }
        }
        
        // Initial load
        loadData();
        
        // Refresh every 5s
        setInterval(loadData, 5000);
    </script>
</body>
</html>`

// dashboardHandler serves the main dashboard
func dashboardHandler(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, dashboardHTML)
}
