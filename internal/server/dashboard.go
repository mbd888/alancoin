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
            --tier-elite: #f59e0b;
            --tier-trusted: #22c55e;
            --tier-established: #3b82f6;
            --tier-emerging: #a1a1aa;
            --tier-new: #52525b;
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
            width: 36px;
            height: 36px;
            border-radius: 6px;
            object-fit: contain;
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
            display: inline-flex;
            align-items: center;
            gap: 6px;
            font-weight: 500;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
            cursor: pointer;
            position: relative;
        }

        .tx-agent:hover .agent-tooltip {
            opacity: 1;
            visibility: visible;
        }

        .agent-avatar {
            width: 20px;
            height: 20px;
            border-radius: 4px;
            flex-shrink: 0;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 10px;
            font-weight: 600;
            color: #fff;
        }

        .agent-tooltip {
            position: absolute;
            bottom: 100%;
            left: 50%;
            transform: translateX(-50%);
            background: var(--bg-subtle);
            border: 1px solid var(--border);
            padding: 6px 10px;
            border-radius: 6px;
            font-size: 11px;
            font-family: 'JetBrains Mono', monospace;
            white-space: nowrap;
            opacity: 0;
            visibility: hidden;
            transition: opacity 0.15s;
            z-index: 100;
            margin-bottom: 4px;
            color: var(--text-secondary);
        }

        .verified-badge {
            color: var(--accent);
            font-size: 12px;
            margin-left: 2px;
        }

        .tier-badge {
            font-size: 11px;
            margin-left: 3px;
        }
        .tier-badge.elite { color: var(--tier-elite); }
        .tier-badge.trusted { color: var(--tier-trusted); }
        .tier-badge.established { color: var(--tier-established); }
        .tier-badge.emerging { color: var(--tier-emerging); }

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
            grid-template-columns: 24px 1fr auto;
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

        /* Trust Distribution */
        .trust-bar-container { margin-bottom: 16px; }
        .trust-bar {
            display: flex;
            height: 28px;
            border-radius: 6px;
            overflow: hidden;
            background: var(--bg);
        }
        .trust-bar-segment {
            transition: width 0.5s ease;
            min-width: 0;
        }
        .trust-legend {
            display: flex;
            flex-wrap: wrap;
            gap: 12px;
            margin-top: 12px;
        }
        .trust-legend-item {
            display: flex;
            align-items: center;
            gap: 6px;
            font-size: 12px;
            color: var(--text-secondary);
        }
        .trust-legend-dot {
            width: 8px;
            height: 8px;
            border-radius: 2px;
            flex-shrink: 0;
        }
        .trust-legend-count {
            color: var(--text-tertiary);
            font-size: 11px;
        }

        /* Credit stats */
        .credit-stat-row {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 8px 0;
            border-bottom: 1px solid var(--border);
        }
        .credit-stat-row:last-child {
            border-bottom: none;
        }
        .credit-stat-label {
            color: var(--text-secondary);
            font-size: 13px;
        }
        .credit-stat-value {
            font-weight: 500;
            font-size: 13px;
        }
        .credit-stat-value.healthy {
            color: var(--accent);
        }
        .credit-stat-value.warning {
            color: var(--tier-elite);
        }
        .credit-stat-value.blue {
            color: var(--blue);
        }
        .credit-util-bar {
            height: 4px;
            background: var(--border);
            border-radius: 2px;
            margin-top: 4px;
            overflow: hidden;
        }
        .credit-util-fill {
            height: 100%;
            border-radius: 2px;
            transition: width 0.5s ease;
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
                <img src="/assets/alancoin_logo.png" class="logo-mark" alt="Alancoin">
                <span class="logo-text">alancoin</span>
            </a>
            <nav>
                <a href="/" class="active">Dashboard</a>
                <a href="/agents">Agents</a>
                <a href="/services">Services</a>
                <a href="/v1/feed">API</a>
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
                <div class="hero-stat">
                    <span class="hero-stat-value mono" id="trusted-count" style="color:var(--tier-trusted)">0</span>
                    <span class="hero-stat-label">trusted+</span>
                </div>
                <div class="hero-stat">
                    <span class="hero-stat-value mono" id="credit-extended" style="color:var(--blue)">$0</span>
                    <span class="hero-stat-label">credit extended</span>
                </div>
                <div class="hero-stat">
                    <span class="hero-stat-value mono" id="escrows-active" style="color:var(--tier-elite)">0</span>
                    <span class="hero-stat-label">escrows</span>
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
                        <span class="section-title">Trust Distribution</span>
                    </div>
                    <div id="trust-distribution">
                        <div class="empty">Loading...</div>
                    </div>
                </div>

                <div class="sidebar-section">
                    <div class="section-header">
                        <span class="section-title">Credit & Escrow</span>
                    </div>
                    <div id="credit-stats">
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
        // Escape HTML to prevent XSS
        function escapeHtml(text) {
            if (text == null) return '';
            const div = document.createElement('div');
            div.textContent = String(text);
            return div.innerHTML;
        }

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
            if (num === undefined || num === null) return '0';
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

        // Generate consistent color from address
        function addressToColor(addr) {
            if (!addr) return '#888';
            const colors = ['#ef4444', '#f97316', '#eab308', '#22c55e', '#14b8a6', '#3b82f6', '#8b5cf6', '#ec4899'];
            let hash = 0;
            for (let i = 0; i < addr.length; i++) {
                hash = addr.charCodeAt(i) + ((hash << 5) - hash);
            }
            return colors[Math.abs(hash) % colors.length];
        }

        // Get initials from name
        function getInitials(name) {
            if (!name) return '?';
            return name.split(/(?=[A-Z])/).map(w => w[0]).join('').slice(0, 2).toUpperCase();
        }

        // Tier badge helper
        const tierIcons = { elite: '\u2605', trusted: '\u25C6', established: '\u25CF', emerging: '\u25CB' };
        const tierColors = { elite: 'var(--tier-elite)', trusted: 'var(--tier-trusted)', established: 'var(--tier-established)', emerging: 'var(--tier-emerging)', new: 'var(--tier-new)' };

        function tierBadge(tier) {
            const icon = tierIcons[tier];
            if (!icon) return '';
            return '<span class="tier-badge ' + tier + '">' + icon + '</span>';
        }

        // Store reputation data keyed by address
        let repByAddr = {};

        // Store agents data for verification lookup
        let cachedAgents = [];

        // Render transaction with avatars
        function renderTx(tx, isNew) {
            const fromColor = addressToColor(tx.fromAddress);
            const toColor = addressToColor(tx.toAddress);
            const fromInitials = getInitials(tx.fromName);
            const toInitials = getInitials(tx.toName);

            return '<div class="tx' + (isNew ? ' new' : '') + '">' +
                '<div class="tx-parties">' +
                    '<span class="tx-agent">' +
                        '<span class="agent-avatar" style="background:' + fromColor + '">' + escapeHtml(fromInitials) + '</span>' +
                        escapeHtml(tx.fromName) +
                        tierBadge((repByAddr[tx.fromAddress] || {}).tier) +
                        '<span class="agent-tooltip">' + escapeHtml(tx.fromAddress) + '</span>' +
                    '</span>' +
                    '<span class="tx-arrow">\u2192</span>' +
                    '<span class="tx-agent">' +
                        '<span class="agent-avatar" style="background:' + toColor + '">' + escapeHtml(toInitials) + '</span>' +
                        escapeHtml(tx.toName) +
                        tierBadge((repByAddr[tx.toAddress] || {}).tier) +
                        '<span class="agent-tooltip">' + escapeHtml(tx.toAddress) + '</span>' +
                    '</span>' +
                    (tx.serviceName ? '<span class="tx-service">' + escapeHtml(tx.serviceName) + '</span>' : '') +
                '</div>' +
                '<span class="tx-amount positive mono">$' + formatUSD(tx.amount) + '</span>' +
                '<span class="tx-time mono">' + timeAgo(tx.timestamp) + '</span>' +
            '</div>';
        }

        // Render agent row with tier badge
        function renderAgent(agent, rank) {
            const topService = agent.services && agent.services[0] ? agent.services[0].type : 'agent';
            const revenue = parseFloat(agent.stats?.totalReceived || 0);
            const txns = agent.stats?.transactionCount || 0;
            const color = addressToColor(agent.address);
            const initials = getInitials(agent.name);
            const rep = repByAddr[agent.address] || {};

            return '<a href="/agent/' + encodeURIComponent(agent.address) + '" class="agent-row" title="' + escapeHtml(agent.address) + '">' +
                '<span class="agent-avatar" style="background:' + color + '">' + escapeHtml(initials) + '</span>' +
                '<div class="agent-info">' +
                    '<div class="agent-name">' + escapeHtml(agent.name) + tierBadge(rep.tier) + '</div>' +
                    '<div class="agent-type">' + escapeHtml(topService) + '</div>' +
                '</div>' +
                '<div class="agent-revenue">' +
                    '<div class="agent-revenue-value mono">$' + formatUSD(revenue) + '</div>' +
                    '<div class="agent-revenue-txns">' + formatCompact(txns) + ' txns</div>' +
                '</div>' +
            '</a>';
        }

        // Render trust distribution bar
        function renderTrustDistribution(tiers) {
            const total = (tiers.elite||0) + (tiers.trusted||0) + (tiers.established||0) + (tiers.emerging||0) + (tiers.new||0);
            if (total === 0) return '<div class="empty">No agents yet</div>';

            function pct(n) { return ((n / total) * 100).toFixed(1); }

            const segments = [
                { tier: 'elite', color: 'var(--tier-elite)', label: '\u2605 Elite', count: tiers.elite || 0 },
                { tier: 'trusted', color: 'var(--tier-trusted)', label: '\u25C6 Trusted', count: tiers.trusted || 0 },
                { tier: 'established', color: 'var(--tier-established)', label: '\u25CF Established', count: tiers.established || 0 },
                { tier: 'emerging', color: 'var(--tier-emerging)', label: '\u25CB Emerging', count: tiers.emerging || 0 },
                { tier: 'new', color: 'var(--tier-new)', label: 'New', count: tiers.new || 0 },
            ];

            let bar = '<div class="trust-bar-container"><div class="trust-bar">';
            segments.forEach(s => {
                if (s.count > 0) {
                    bar += '<div class="trust-bar-segment" style="width:' + pct(s.count) + '%;background:' + s.color + '" title="' + s.label + ': ' + s.count + '"></div>';
                }
            });
            bar += '</div><div class="trust-legend">';
            segments.forEach(s => {
                if (s.count > 0) {
                    bar += '<div class="trust-legend-item"><span class="trust-legend-dot" style="background:' + s.color + '"></span>' + s.label + ' <span class="trust-legend-count">' + s.count + '</span></div>';
                }
            });
            bar += '</div></div>';
            return bar;
        }

        // Safe fetch that returns null on error
        async function safeFetch(url) {
            try {
                const r = await fetch(url);
                if (!r.ok) return null;
                return await r.json();
            } catch (e) {
                return null;
            }
        }

        // Render credit & escrow stats section
        function renderCreditStats(creditRes, agentsRes) {
            const el = document.getElementById('credit-stats');
            if (!el) return;

            let lines = creditRes?.credit_lines || [];
            let activeLines = lines.length;
            let totalExtended = 0;
            let totalUsed = 0;

            lines.forEach(cl => {
                totalExtended += parseFloat(cl.creditLimit || 0);
                totalUsed += parseFloat(cl.creditUsed || 0);
            });

            let utilPct = totalExtended > 0 ? (totalUsed / totalExtended * 100) : 0;
            let utilColor = utilPct < 50 ? 'var(--accent)' : utilPct < 80 ? 'var(--tier-elite)' : 'var(--red)';
            let utilClass = utilPct < 50 ? 'healthy' : 'warning';

            // Update hero metrics
            const creditExtEl = document.getElementById('credit-extended');
            if (creditExtEl) creditExtEl.textContent = '$' + formatUSD(totalExtended);

            let html = '';
            html += '<div class="credit-stat-row"><span class="credit-stat-label">Active Credit Lines</span><span class="credit-stat-value blue">' + activeLines + '</span></div>';
            html += '<div class="credit-stat-row"><span class="credit-stat-label">Total Extended</span><span class="credit-stat-value">$' + formatUSD(totalExtended) + '</span></div>';
            html += '<div class="credit-stat-row"><span class="credit-stat-label">Total Used</span><span class="credit-stat-value">$' + formatUSD(totalUsed) + '</span></div>';
            html += '<div class="credit-stat-row"><span class="credit-stat-label">Utilization</span><span class="credit-stat-value ' + utilClass + '">' + utilPct.toFixed(1) + '%</span></div>';
            html += '<div class="credit-util-bar"><div class="credit-util-fill" style="width:' + Math.min(utilPct, 100) + '%;background:' + utilColor + '"></div></div>';

            if (activeLines === 0) {
                html = '<div class="empty" style="padding:16px">No active credit lines</div>';
            }

            el.innerHTML = html;
        }

        // Fetch and render
        async function loadData() {
            try {
                const [statsRes, feedRes, agentsRes, repRes, creditRes] = await Promise.all([
                    safeFetch('/v1/network/stats'),
                    safeFetch('/v1/feed?limit=15'),
                    safeFetch('/v1/agents?limit=10'),
                    safeFetch('/v1/reputation?limit=100'),
                    safeFetch('/v1/credit/active')
                ]);

                // Build reputation lookup
                if (repRes?.leaderboard) {
                    repByAddr = {};
                    repRes.leaderboard.forEach(e => { repByAddr[e.address] = { score: e.score, tier: e.tier }; });
                }

                // Cache agents
                cachedAgents = agentsRes?.agents || [];

                // Stats
                if (statsRes) {
                    document.getElementById('total-volume').textContent = formatUSD(statsRes.totalVolume || 0);
                    document.getElementById('total-txns').textContent = formatCompact(statsRes.totalTransactions || 0);
                    document.getElementById('total-agents').textContent = formatCompact(statsRes.totalAgents || 0);
                    document.getElementById('total-services').textContent = formatCompact(statsRes.totalServices || 0);
                }

                // Trusted+ count (trusted + elite)
                if (repRes?.tiers) {
                    const t = repRes.tiers;
                    document.getElementById('trusted-count').textContent = formatCompact((t.trusted||0) + (t.elite||0));
                }

                // Transactions
                const txStream = document.getElementById('tx-stream');
                if (feedRes?.feed && feedRes.feed.length > 0) {
                    txStream.innerHTML = feedRes.feed.map(tx => renderTx(tx)).join('');
                } else if (feedRes) {
                    txStream.innerHTML = '<div class="empty">No transactions yet</div>';
                }

                // Top agents (sort by revenue)
                const topAgents = document.getElementById('top-agents');
                if (agentsRes?.agents && agentsRes.agents.length > 0) {
                    const sorted = [...agentsRes.agents].sort((a, b) => {
                        const aRev = parseFloat(a.stats?.totalReceived || 0);
                        const bRev = parseFloat(b.stats?.totalReceived || 0);
                        return bRev - aRev;
                    }).slice(0, 5);
                    topAgents.innerHTML = sorted.map((a, i) => renderAgent(a, i + 1)).join('');
                } else if (agentsRes) {
                    topAgents.innerHTML = '<div class="empty">No agents yet</div>';
                }

                // Trust distribution
                const trustDist = document.getElementById('trust-distribution');
                if (repRes?.tiers) {
                    trustDist.innerHTML = renderTrustDistribution(repRes.tiers);
                }

                // Credit & Escrow stats
                renderCreditStats(creditRes, agentsRes);

                // Escrow count from top agents (count active escrows)
                const escrowEl = document.getElementById('escrows-active');
                if (escrowEl && agentsRes?.agents) {
                    // Fetch escrow counts for top agents
                    let escrowCount = 0;
                    const topAddrs = agentsRes.agents.slice(0, 5).map(a => a.address);
                    const escrowResults = await Promise.all(
                        topAddrs.map(addr => safeFetch('/v1/agents/' + addr + '/escrows?limit=50'))
                    );
                    escrowResults.forEach(res => {
                        if (res?.escrows) {
                            escrowCount += res.escrows.filter(e => e.status === 'pending' || e.status === 'delivered').length;
                        }
                    });
                    escrowEl.textContent = formatCompact(escrowCount);
                }

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
