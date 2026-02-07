package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// debugPageHandler serves a simple debug page to test API connectivity
func debugPageHandler(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, debugPageHTML)
}

const debugPageHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Alancoin Debug</title>
    <style>
        body { font-family: monospace; background: #111; color: #0f0; padding: 20px; }
        pre { background: #222; padding: 10px; overflow: auto; }
        .error { color: #f00; }
        .success { color: #0f0; }
        h2 { color: #0ff; margin-top: 20px; }
    </style>
</head>
<body>
    <h1>Alancoin Debug Page</h1>
    <p>Testing API connectivity...</p>

    <h2>1. Network Stats (/v1/network/stats)</h2>
    <pre id="stats">Loading...</pre>

    <h2>2. Feed (/v1/feed?limit=3)</h2>
    <pre id="feed">Loading...</pre>

    <h2>3. Agents (/v1/agents?limit=3)</h2>
    <pre id="agents">Loading...</pre>

    <h2>4. Services (/v1/services?limit=3)</h2>
    <pre id="services">Loading...</pre>

    <script>
        async function test(endpoint, elementId) {
            const el = document.getElementById(elementId);
            try {
                const res = await fetch(endpoint);
                const data = await res.json();
                el.className = 'success';
                el.textContent = JSON.stringify(data, null, 2);
            } catch (e) {
                el.className = 'error';
                el.textContent = 'ERROR: ' + e.message;
            }
        }

        test('/v1/network/stats', 'stats');
        test('/v1/feed?limit=3', 'feed');
        test('/v1/agents?limit=3', 'agents');
        test('/v1/services?limit=3', 'services');
    </script>
</body>
</html>`
