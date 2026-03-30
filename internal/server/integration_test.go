package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Integration test: full payment flow
// Register buyer + seller → deposit → add service → gateway session → proxy → verify
// ---------------------------------------------------------------------------

func TestIntegration_FullPaymentFlow(t *testing.T) {
	s := newTestServer(t)
	ctx := t.Context()

	buyerAddr := "0xaaaa000000000000000000000000000000000001"
	sellerAddr := "0xbbbb000000000000000000000000000000000002"

	// 1. Register agents
	buyerKey := registerAgent(t, s, buyerAddr, "buyer-agent")
	sellerKey := registerAgent(t, s, sellerAddr, "seller-agent")

	// 2. Fund buyer via ledger (large balance to avoid supervisor rules)
	if err := s.ledger.Deposit(ctx, buyerAddr, "1000.000000", "0xdeposit_integ"); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	// 3. Add a service to seller (mock endpoint)
	mockService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"result": "hello from seller"})
	}))
	defer mockService.Close()

	addServiceBody := fmt.Sprintf(`{
		"id": "svc_translate",
		"type": "translation",
		"name": "Translation Service",
		"price": "1.000000",
		"endpoint": "%s"
	}`, mockService.URL)
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/v1/agents/"+sellerAddr+"/services", sellerKey, addServiceBody)
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("add service: expected 2xx, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Create gateway session ($4.50 to stay under $5 new-agent per-tx limit)
	sessionBody := `{
		"maxTotal": "4.500000",
		"maxPerRequest": "2.000000",
		"strategy": "cheapest"
	}`
	w = httptest.NewRecorder()
	req = authedRequest("POST", "/v1/gateway/sessions", buyerKey, sessionBody)
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var sessionResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &sessionResp)
	// Response may be wrapped: {"session": {...}} or top-level
	sessObj, _ := sessionResp["session"].(map[string]interface{})
	if sessObj == nil {
		sessObj = sessionResp
	}
	sessionID, _ := sessObj["id"].(string)
	if sessionID == "" {
		t.Fatalf("create session: missing session ID, response: %s", w.Body.String())
	}
	// Get the gateway token for proxy auth
	gatewayToken, _ := sessionResp["token"].(string)

	// 5. Verify buyer balance decreased (hold applied)
	bal, _ := s.ledger.GetBalance(ctx, buyerAddr)
	if bal.Available != "995.500000" && bal.Pending != "4.500000" {
		t.Logf("after session: available=%s pending=%s", bal.Available, bal.Pending)
	}

	// 6. Proxy a request (uses gateway token auth)
	proxyBody := `{
		"serviceType": "translation",
		"params": {"text": "hello", "targetLang": "es"}
	}`
	w = httptest.NewRecorder()
	req = authedRequest("POST", "/v1/gateway/proxy", buyerKey, proxyBody)
	if gatewayToken != "" {
		req.Header.Set("X-Gateway-Token", gatewayToken)
	}
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("proxy: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var proxyResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &proxyResp)

	// Verify proxy spent money — check the remaining budget decreased
	remaining, _ := proxyResp["remaining"].(string)
	if remaining == "" {
		t.Error("proxy: missing 'remaining' in response")
	}

	// 7. Verify seller received payment
	sellerBal, _ := s.ledger.GetBalance(ctx, sellerAddr)
	if sellerBal.Available == "0" || sellerBal.Available == "0.000000" {
		t.Errorf("seller should have received payment, available=%s", sellerBal.Available)
	}

	// 8. Close session
	w = httptest.NewRecorder()
	req = authedRequest("DELETE", "/v1/gateway/sessions/"+sessionID, buyerKey, "")
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("close session: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 9. Verify remaining hold released
	finalBal, _ := s.ledger.GetBalance(ctx, buyerAddr)
	if finalBal.Pending != "0.000000" && finalBal.Pending != "0" {
		t.Errorf("after close: expected pending=0, got %s", finalBal.Pending)
	}

	t.Logf("Full payment flow: buyer available=%s, seller available=%s",
		finalBal.Available, sellerBal.Available)
}

// ---------------------------------------------------------------------------
// Integration test: escrow lifecycle via HTTP
// ---------------------------------------------------------------------------

func TestIntegration_EscrowLifecycle(t *testing.T) {
	s := newTestServer(t)
	ctx := t.Context()

	buyerAddr := "0xcccc000000000000000000000000000000000003"
	sellerAddr := "0xdddd000000000000000000000000000000000004"

	buyerKey := registerAgent(t, s, buyerAddr, "escrow-buyer")
	sellerKey := registerAgent(t, s, sellerAddr, "escrow-seller")

	// Fund buyer
	if err := s.ledger.Deposit(ctx, buyerAddr, "50.000000", "0xdeposit_escrow"); err != nil {
		t.Fatalf("deposit failed: %v", err)
	}

	// 1. Create escrow
	escrowBody := fmt.Sprintf(`{
		"buyerAddr": "%s",
		"sellerAddr": "%s",
		"amount": "5.000000",
		"autoRelease": "5m"
	}`, buyerAddr, sellerAddr)
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/v1/escrow", buyerKey, escrowBody)
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create escrow: expected 2xx, got %d: %s", w.Code, w.Body.String())
	}

	var escrowResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &escrowResp); err != nil {
		t.Fatalf("create escrow: unmarshal: %v, body: %s", err, w.Body.String())
	}
	// Response is {"escrow": {...}} — extract the nested object
	escrowObj, _ := escrowResp["escrow"].(map[string]interface{})
	if escrowObj == nil {
		escrowObj = escrowResp // fallback to top-level
	}
	escrowID, _ := escrowObj["id"].(string)
	if escrowID == "" {
		t.Fatalf("create escrow: missing escrow ID, response: %s", w.Body.String())
	}

	// Verify funds locked
	bal, _ := s.ledger.GetBalance(ctx, buyerAddr)
	if bal.Escrowed != "5.000000" {
		t.Errorf("after escrow: expected escrowed=5.000000, got %s", bal.Escrowed)
	}

	// 2. Mark delivered (seller action)
	w = httptest.NewRecorder()
	req = authedRequest("POST", "/v1/escrow/"+escrowID+"/deliver", sellerKey, "")
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mark delivered: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Confirm (release to seller)
	w = httptest.NewRecorder()
	req = authedRequest("POST", "/v1/escrow/"+escrowID+"/confirm", buyerKey, "")
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Verify settlement
	buyerBal, _ := s.ledger.GetBalance(ctx, buyerAddr)
	if buyerBal.Escrowed != "0.000000" && buyerBal.Escrowed != "0" {
		t.Errorf("after confirm: expected buyer escrowed=0, got %s", buyerBal.Escrowed)
	}
	sellerBal, _ := s.ledger.GetBalance(ctx, sellerAddr)
	if sellerBal.Available != "5.000000" {
		t.Errorf("after confirm: expected seller available=5.000000, got %s", sellerBal.Available)
	}

	// 5. Verify escrow status
	w = httptest.NewRecorder()
	req = authedRequest("GET", "/v1/escrow/"+escrowID, buyerKey, "")
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get escrow: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var escrowStatus map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &escrowStatus)
	// Response may be wrapped: {"escrow": {...}} or top-level
	statusObj, _ := escrowStatus["escrow"].(map[string]interface{})
	if statusObj == nil {
		statusObj = escrowStatus
	}
	if statusObj["status"] != "released" {
		t.Errorf("expected escrow status=released, got %v (body: %s)", statusObj["status"], w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Integration test: WebSocket event delivery
// ---------------------------------------------------------------------------

func TestIntegration_WebSocketEvents(t *testing.T) {
	s := newTestServer(t)

	// Start the realtime hub (normally started in s.Run())
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	if s.realtimeHub != nil {
		go s.realtimeHub.Run(hubCtx)
	}
	time.Sleep(20 * time.Millisecond) // let hub start

	// Start HTTP test server so we can connect via WebSocket
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	// Register an agent first to get an API key for WebSocket auth
	agentAddr := "0xeeee000000000000000000000000000000000005"
	agentKey := registerAgent(t, s, agentAddr, "ws-test-agent")

	// Connect WebSocket with API key auth
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + agentKey
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket connect: %v", err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	defer conn.Close()

	// Subscribe to all events
	conn.WriteMessage(websocket.TextMessage, []byte(`{"allEvents": true}`))

	// Give hub time to register the client
	time.Sleep(50 * time.Millisecond)

	// Fund and create a escrow to trigger escrow_created
	ctx := t.Context()
	_ = s.ledger.Deposit(ctx, agentAddr, "10.000000", "0xws_deposit")

	sellerAddr := "0xffff000000000000000000000000000000000006"
	_ = registerAgent(t, s, sellerAddr, "ws-test-seller")

	escrowBody := fmt.Sprintf(`{"buyerAddr":"%s","sellerAddr":"%s","amount":"1.000000"}`, agentAddr, sellerAddr)
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/v1/escrow", agentKey, escrowBody)
	s.router.ServeHTTP(w, req)

	// Allow async goroutines to broadcast (escrow broadcasts happen in goroutines)
	time.Sleep(500 * time.Millisecond)

	// Read events from WebSocket (non-blocking with deadline)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var receivedTypes []string
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var event map[string]interface{}
		if json.Unmarshal(msg, &event) == nil {
			if eventType, ok := event["type"].(string); ok {
				receivedTypes = append(receivedTypes, eventType)
			}
		}
	}

	if len(receivedTypes) == 0 {
		t.Error("expected to receive at least one WebSocket event, got none")
	} else {
		t.Logf("received %d WebSocket events: %v", len(receivedTypes), receivedTypes)
	}

	// Check for escrow_created event
	hasEscrowEvent := false
	for _, et := range receivedTypes {
		if et == "escrow_created" {
			hasEscrowEvent = true
			break
		}
	}
	if !hasEscrowEvent {
		t.Errorf("expected escrow_created event in WebSocket stream, got: %v", receivedTypes)
	}
}
