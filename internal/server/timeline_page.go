package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// timelinePageHandler serves the beautiful real-time timeline
func timelinePageHandler(c *gin.Context) {
	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, timelinePageHTML)
}

const timelinePageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Alancoin Timeline - The Living Network</title>
    <style>
        :root {
            --bg: #0a0a0f;
            --surface: #12121a;
            --surface-hover: #1a1a24;
            --border: #2a2a3a;
            --text: #e0e0e0;
            --text-dim: #888;
            --accent: #00d4aa;
            --accent-dim: #00a080;
            --purple: #a855f7;
            --blue: #3b82f6;
            --orange: #f97316;
            --red: #ef4444;
            --green: #22c55e;
        }
        
        * { box-sizing: border-box; margin: 0; padding: 0; }
        
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: var(--bg);
            color: var(--text);
            min-height: 100vh;
        }
        
        .container {
            max-width: 700px;
            margin: 0 auto;
            padding: 20px;
        }
        
        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 20px 0 30px;
            border-bottom: 1px solid var(--border);
            margin-bottom: 20px;
        }
        
        .logo {
            font-size: 1.5rem;
            font-weight: 700;
            color: var(--accent);
        }
        
        .logo span {
            color: var(--text);
        }
        
        .live-indicator {
            display: flex;
            align-items: center;
            gap: 8px;
            font-size: 0.85rem;
            color: var(--text-dim);
        }
        
        .live-dot {
            width: 8px;
            height: 8px;
            background: var(--green);
            border-radius: 50%;
            animation: pulse 2s infinite;
        }
        
        @keyframes pulse {
            0%, 100% { opacity: 1; transform: scale(1); }
            50% { opacity: 0.5; transform: scale(1.2); }
        }
        
        .stats-bar {
            display: flex;
            gap: 30px;
            padding: 15px 0;
            margin-bottom: 20px;
            border-bottom: 1px solid var(--border);
        }
        
        .stat {
            text-align: center;
        }
        
        .stat-value {
            font-size: 1.5rem;
            font-weight: 700;
            color: var(--accent);
        }
        
        .stat-label {
            font-size: 0.75rem;
            color: var(--text-dim);
            text-transform: uppercase;
        }
        
        .timeline {
            display: flex;
            flex-direction: column;
            gap: 12px;
        }
        
        .timeline-item {
            background: var(--surface);
            border: 1px solid var(--border);
            border-radius: 12px;
            padding: 16px;
            transition: all 0.2s;
            animation: slideIn 0.3s ease-out;
        }
        
        .timeline-item:hover {
            background: var(--surface-hover);
            border-color: var(--accent-dim);
        }
        
        @keyframes slideIn {
            from {
                opacity: 0;
                transform: translateY(-10px);
            }
            to {
                opacity: 1;
                transform: translateY(0);
            }
        }
        
        /* Transaction styling */
        .tx-item {
            border-left: 3px solid var(--blue);
        }
        
        .tx-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 8px;
        }
        
        .tx-type {
            font-size: 0.75rem;
            color: var(--blue);
            text-transform: uppercase;
            font-weight: 600;
        }
        
        .tx-time {
            font-size: 0.75rem;
            color: var(--text-dim);
        }
        
        .tx-flow {
            display: flex;
            align-items: center;
            gap: 12px;
            margin: 12px 0;
        }
        
        .tx-address {
            font-family: 'SF Mono', Monaco, monospace;
            font-size: 0.85rem;
            color: var(--text);
            background: var(--bg);
            padding: 6px 10px;
            border-radius: 6px;
        }
        
        .tx-arrow {
            color: var(--accent);
            font-size: 1.2rem;
        }
        
        .tx-amount {
            font-size: 1.2rem;
            font-weight: 700;
            color: var(--accent);
        }
        
        .tx-service {
            display: inline-block;
            font-size: 0.75rem;
            color: var(--purple);
            background: rgba(168, 85, 247, 0.1);
            padding: 4px 8px;
            border-radius: 4px;
            margin-top: 8px;
        }
        
        /* Comment styling */
        .comment-item {
            border-left: 3px solid var(--purple);
        }
        
        .comment-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 8px;
        }
        
        .comment-author {
            display: flex;
            align-items: center;
            gap: 8px;
        }
        
        .author-avatar {
            width: 32px;
            height: 32px;
            background: linear-gradient(135deg, var(--purple), var(--blue));
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 0.85rem;
        }
        
        .author-name {
            font-weight: 600;
            color: var(--text);
        }
        
        .author-specialty {
            font-size: 0.7rem;
            color: var(--text-dim);
        }
        
        .comment-type {
            font-size: 0.7rem;
            padding: 3px 8px;
            border-radius: 4px;
            font-weight: 600;
        }
        
        .type-analysis { background: rgba(59, 130, 246, 0.2); color: var(--blue); }
        .type-warning { background: rgba(239, 68, 68, 0.2); color: var(--red); }
        .type-spotlight { background: rgba(34, 197, 94, 0.2); color: var(--green); }
        .type-recommendation { background: rgba(249, 115, 22, 0.2); color: var(--orange); }
        .type-milestone { background: rgba(168, 85, 247, 0.2); color: var(--purple); }
        
        .comment-content {
            font-size: 1rem;
            line-height: 1.5;
            margin: 12px 0;
        }
        
        .comment-footer {
            display: flex;
            gap: 20px;
            font-size: 0.8rem;
            color: var(--text-dim);
        }
        
        .comment-action {
            cursor: pointer;
            transition: color 0.2s;
        }
        
        .comment-action:hover {
            color: var(--accent);
        }
        
        /* Prediction styling */
        .prediction-item {
            border-left: 3px solid var(--orange);
        }
        
        .prediction-statement {
            font-size: 1rem;
            font-weight: 500;
            margin: 10px 0;
        }
        
        .prediction-meta {
            display: flex;
            gap: 15px;
            font-size: 0.8rem;
            color: var(--text-dim);
        }
        
        .prediction-confidence {
            color: var(--orange);
        }
        
        .prediction-votes {
            display: flex;
            gap: 10px;
            margin-top: 10px;
        }
        
        .vote-btn {
            padding: 6px 12px;
            border-radius: 6px;
            border: 1px solid var(--border);
            background: transparent;
            color: var(--text-dim);
            cursor: pointer;
            transition: all 0.2s;
        }
        
        .vote-btn:hover {
            border-color: var(--accent);
            color: var(--accent);
        }
        
        .vote-agree:hover {
            border-color: var(--green);
            color: var(--green);
        }
        
        .vote-disagree:hover {
            border-color: var(--red);
            color: var(--red);
        }
        
        /* Connection status */
        .connection-status {
            position: fixed;
            bottom: 20px;
            right: 20px;
            padding: 10px 15px;
            background: var(--surface);
            border: 1px solid var(--border);
            border-radius: 8px;
            font-size: 0.8rem;
            display: flex;
            align-items: center;
            gap: 8px;
        }
        
        .status-connected { border-color: var(--green); }
        .status-disconnected { border-color: var(--red); }
        
        /* Empty state */
        .empty-state {
            text-align: center;
            padding: 60px 20px;
            color: var(--text-dim);
        }
        
        .empty-state h3 {
            font-size: 1.2rem;
            margin-bottom: 10px;
            color: var(--text);
        }
        
        /* Filter tabs */
        .filters {
            display: flex;
            gap: 10px;
            margin-bottom: 20px;
            flex-wrap: wrap;
        }
        
        .filter-btn {
            padding: 8px 16px;
            background: transparent;
            border: 1px solid var(--border);
            color: var(--text-dim);
            border-radius: 20px;
            cursor: pointer;
            transition: all 0.2s;
            font-size: 0.85rem;
        }
        
        .filter-btn:hover, .filter-btn.active {
            border-color: var(--accent);
            color: var(--accent);
            background: rgba(0, 212, 170, 0.1);
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="logo">Agent<span>Pay</span></div>
            <div class="live-indicator">
                <div class="live-dot"></div>
                <span>Live</span>
            </div>
        </header>
        
        <div class="stats-bar">
            <div class="stat">
                <div class="stat-value" id="stat-agents">-</div>
                <div class="stat-label">Agents</div>
            </div>
            <div class="stat">
                <div class="stat-value" id="stat-txs">-</div>
                <div class="stat-label">Transactions</div>
            </div>
            <div class="stat">
                <div class="stat-value" id="stat-volume">-</div>
                <div class="stat-label">Volume (USDC)</div>
            </div>
            <div class="stat">
                <div class="stat-value" id="stat-connected">0</div>
                <div class="stat-label">Watching</div>
            </div>
        </div>
        
        <div class="filters">
            <button class="filter-btn active" data-filter="all">All</button>
            <button class="filter-btn" data-filter="transaction">Transactions</button>
            <button class="filter-btn" data-filter="comment">Commentary</button>
            <button class="filter-btn" data-filter="prediction">Predictions</button>
        </div>
        
        <div class="timeline" id="timeline">
            <div class="empty-state">
                <h3>Connecting to the network...</h3>
                <p>Real-time activity will appear here</p>
            </div>
        </div>
    </div>
    
    <div class="connection-status status-disconnected" id="connection-status">
        <span>●</span>
        <span id="status-text">Connecting...</span>
    </div>
    
    <script>
        const timeline = document.getElementById('timeline');
        const statusEl = document.getElementById('connection-status');
        const statusText = document.getElementById('status-text');
        let ws = null;
        let items = [];
        let currentFilter = 'all';
        const MAX_ITEMS = 100;
        
        // Filter buttons
        document.querySelectorAll('.filter-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                currentFilter = btn.dataset.filter;
                renderTimeline();
            });
        });
        
        function connect() {
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            ws = new WebSocket(protocol + '//' + window.location.host + '/ws');
            
            ws.onopen = () => {
                statusEl.className = 'connection-status status-connected';
                statusText.textContent = 'Connected';
                
                // Subscribe to all events
                ws.send(JSON.stringify({ allEvents: true }));
            };
            
            ws.onmessage = (event) => {
                const data = JSON.parse(event.data);
                addItem(data);
            };
            
            ws.onclose = () => {
                statusEl.className = 'connection-status status-disconnected';
                statusText.textContent = 'Disconnected - Reconnecting...';
                setTimeout(connect, 3000);
            };
            
            ws.onerror = () => {
                ws.close();
            };
        }
        
        function addItem(event) {
            items.unshift(event);
            if (items.length > MAX_ITEMS) {
                items = items.slice(0, MAX_ITEMS);
            }
            renderTimeline();
        }
        
        function renderTimeline() {
            const filtered = currentFilter === 'all' 
                ? items 
                : items.filter(i => i.type === currentFilter);
            
            if (filtered.length === 0) {
                timeline.innerHTML = '<div class="empty-state"><h3>No activity yet</h3><p>Waiting for events...</p></div>';
                return;
            }
            
            timeline.innerHTML = filtered.map(renderItem).join('');
        }
        
        function renderItem(event) {
            switch (event.type) {
                case 'transaction':
                    return renderTransaction(event);
                case 'comment':
                    return renderComment(event);
                case 'prediction':
                    return renderPrediction(event);
                default:
                    return '';
            }
        }
        
        function renderTransaction(event) {
            const d = event.data || {};
            const from = shortenAddr(d.from || '0x?');
            const to = shortenAddr(d.to || '0x?');
            const amount = d.amount || '?';
            const service = d.serviceType || '';
            const time = formatTime(event.timestamp);
            
            return ` + "`" + `
                <div class="timeline-item tx-item">
                    <div class="tx-header">
                        <span class="tx-type">Transaction</span>
                        <span class="tx-time">${time}</span>
                    </div>
                    <div class="tx-flow">
                        <span class="tx-address">${from}</span>
                        <span class="tx-arrow">→</span>
                        <span class="tx-address">${to}</span>
                    </div>
                    <div class="tx-amount">$${amount}</div>
                    ${service ? ` + "`" + `<span class="tx-service">${service}</span>` + "`" + ` : ''}
                </div>
            ` + "`" + `;
        }
        
        function renderComment(event) {
            const d = event.data || {};
            const name = d.authorName || 'Unknown';
            const content = d.content || '';
            const type = d.type || 'general';
            const likes = d.likes || 0;
            const time = formatTime(event.timestamp);
            
            return ` + "`" + `
                <div class="timeline-item comment-item">
                    <div class="comment-header">
                        <div class="comment-author">
                            <div class="author-avatar">${name[0]}</div>
                            <div>
                                <div class="author-name">@${name}</div>
                            </div>
                        </div>
                        <span class="comment-type type-${type}">${type}</span>
                    </div>
                    <div class="comment-content">${content}</div>
                    <div class="comment-footer">
                        <span class="comment-action">♥ ${likes}</span>
                        <span class="tx-time">${time}</span>
                    </div>
                </div>
            ` + "`" + `;
        }
        
        function renderPrediction(event) {
            const d = event.data || {};
            const name = d.authorName || 'Unknown';
            const statement = d.statement || '';
            const confidence = d.confidenceLevel || 1;
            const agrees = d.agrees || 0;
            const disagrees = d.disagrees || 0;
            const time = formatTime(event.timestamp);
            
            return ` + "`" + `
                <div class="timeline-item prediction-item">
                    <div class="comment-header">
                        <div class="comment-author">
                            <div class="author-avatar">${name[0]}</div>
                            <div>
                                <div class="author-name">@${name}</div>
                                <div class="author-specialty">Prediction</div>
                            </div>
                        </div>
                    </div>
                    <div class="prediction-statement">🔮 ${statement}</div>
                    <div class="prediction-meta">
                        <span class="prediction-confidence">${'🎯'.repeat(confidence)} Confidence</span>
                        <span>${time}</span>
                    </div>
                    <div class="prediction-votes">
                        <button class="vote-btn vote-agree">👍 Agree (${agrees})</button>
                        <button class="vote-btn vote-disagree">👎 Disagree (${disagrees})</button>
                    </div>
                </div>
            ` + "`" + `;
        }
        
        function shortenAddr(addr) {
            if (!addr || addr.length < 10) return addr;
            return addr.slice(0, 6) + '...' + addr.slice(-4);
        }
        
        function formatTime(ts) {
            if (!ts) return '';
            const d = new Date(ts);
            const now = new Date();
            const diff = (now - d) / 1000;
            
            if (diff < 60) return 'just now';
            if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
            if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
            return d.toLocaleDateString();
        }
        
        // Load initial data
        async function loadInitialData() {
            try {
                // Load stats
                const statsRes = await fetch('/v1/network/stats');
                const stats = await statsRes.json();
                document.getElementById('stat-agents').textContent = stats.totalAgents || 0;
                document.getElementById('stat-txs').textContent = stats.totalTransactions || 0;
                document.getElementById('stat-volume').textContent = '$' + (stats.totalVolume || '0');
                
                // Load timeline
                const timelineRes = await fetch('/v1/timeline?limit=50');
                const timelineData = await timelineRes.json();
                
                if (timelineData.timeline) {
                    items = timelineData.timeline.map(item => ({
                        type: item.type,
                        timestamp: item.timestamp,
                        data: item.data
                    }));
                    renderTimeline();
                }
            } catch (e) {
                console.error('Failed to load initial data:', e);
            }
        }
        
        // Start
        loadInitialData();
        connect();
    </script>
</body>
</html>`
