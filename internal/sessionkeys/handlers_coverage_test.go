package sessionkeys

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func testHandler() *Handler {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	return NewHandler(mgr, slog.Default())
}

func testHandlerWithKey(t *testing.T) (*Handler, *SessionKey) {
	t.Helper()
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	h := NewHandler(mgr, slog.Default())

	key, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	return h, key
}

func setupRouter(h *Handler) http.Handler {
	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	return r
}

// --- Handler constructor tests ---

func TestNewHandlerWithExecution(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	h := NewHandlerWithExecution(mgr, nil, nil, nil, slog.Default())
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestHandler_WithMethods(t *testing.T) {
	h := testHandler()

	if got := h.WithDemoMode(); got != h {
		t.Error("WithDemoMode should return same handler")
	}
	if !h.demoMode {
		t.Error("expected demoMode true")
	}
	if got := h.WithEvents(nil); got != h {
		t.Error("WithEvents should return same handler")
	}
	if got := h.WithRevenueAccumulator(nil); got != h {
		t.Error("WithRevenueAccumulator should return same handler")
	}
	if got := h.WithAlertChecker(nil); got != h {
		t.Error("WithAlertChecker should return same handler")
	}
	if got := h.WithReceiptIssuer(nil); got != h {
		t.Error("WithReceiptIssuer should return same handler")
	}
}

// --- CreateSessionKey handler tests ---

func TestHandler_CreateSessionKey_Success(t *testing.T) {
	h := testHandler()

	body := `{"publicKey":"0x1234567890123456789012345678901234567890","expiresIn":"1h","allowAny":true}`
	req := httptest.NewRequest("POST", "/v1/agents/0xowner/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateSessionKey_InvalidBody(t *testing.T) {
	h := testHandler()

	req := httptest.NewRequest("POST", "/v1/agents/0xowner/sessions", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_CreateSessionKey_MissingPublicKey(t *testing.T) {
	h := testHandler()

	body := `{"expiresIn":"1h","allowAny":true}`
	req := httptest.NewRequest("POST", "/v1/agents/0xowner/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateSessionKey_ValidationError(t *testing.T) {
	h := testHandler()

	// No recipients, no service types, and not allowAny -> validation error
	body := `{"publicKey":"0x1234567890123456789012345678901234567890","expiresIn":"1h"}`
	req := httptest.NewRequest("POST", "/v1/agents/0xowner/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// --- ListSessionKeys handler tests ---

func TestHandler_ListSessionKeys_Success(t *testing.T) {
	h, _ := testHandlerWithKey(t)

	req := httptest.NewRequest("GET", "/v1/agents/0x1234567890123456789012345678901234567890/sessions", nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	count := int(resp["count"].(float64))
	if count != 1 {
		t.Errorf("expected 1 key, got %d", count)
	}
}

func TestHandler_ListSessionKeys_Empty(t *testing.T) {
	h := testHandler()

	req := httptest.NewRequest("GET", "/v1/agents/0xnokeys/sessions", nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- GetSessionKey handler tests ---

func TestHandler_GetSessionKey_Success(t *testing.T) {
	h, key := testHandlerWithKey(t)

	req := httptest.NewRequest("GET", "/v1/agents/0xowner/sessions/"+key.ID, nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "active" {
		t.Errorf("expected status active, got %v", resp["status"])
	}
}

func TestHandler_GetSessionKey_NotFound(t *testing.T) {
	h := testHandler()

	req := httptest.NewRequest("GET", "/v1/agents/0xowner/sessions/nonexistent", nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_GetSessionKey_Revoked(t *testing.T) {
	h, key := testHandlerWithKey(t)

	h.manager.Revoke(context.Background(), key.ID)

	req := httptest.NewRequest("GET", "/v1/agents/0xowner/sessions/"+key.ID, nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "revoked" {
		t.Errorf("expected status revoked, got %v", resp["status"])
	}
}

func TestHandler_GetSessionKey_Expired(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	h := NewHandler(mgr, slog.Default())

	key, _ := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1ms",
		AllowAny:  true,
	})
	time.Sleep(5 * time.Millisecond)

	req := httptest.NewRequest("GET", "/v1/agents/0xowner/sessions/"+key.ID, nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "expired" {
		t.Errorf("expected status expired, got %v", resp["status"])
	}
}

// --- RevokeSessionKey handler tests ---

func TestHandler_RevokeSessionKey_Success(t *testing.T) {
	h, key := testHandlerWithKey(t)

	req := httptest.NewRequest("DELETE", "/v1/agents/0xowner/sessions/"+key.ID, nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["keyId"] != key.ID {
		t.Errorf("expected keyId %s, got %v", key.ID, resp["keyId"])
	}
}

func TestHandler_RevokeSessionKey_NotFound(t *testing.T) {
	h := testHandler()

	req := httptest.NewRequest("DELETE", "/v1/agents/0xowner/sessions/nonexistent", nil)
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- Transact handler tests ---

func TestHandler_Transact_InvalidBody(t *testing.T) {
	h, key := testHandlerWithKey(t)

	req := httptest.NewRequest("POST",
		"/v1/agents/0x1234567890123456789012345678901234567890/sessions/"+key.ID+"/transact",
		strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_Transact_KeyNotFound(t *testing.T) {
	h := testHandler()

	body := `{"to":"0xaaaa000000000000000000000000000000000000","amount":"1.00","nonce":1,"timestamp":9999999999,"signature":"0xdead"}`
	req := httptest.NewRequest("POST",
		"/v1/agents/0x1234/sessions/nonexistent/transact",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Transact_WrongOwner(t *testing.T) {
	h, key := testHandlerWithKey(t)

	body := `{"to":"0xaaaa000000000000000000000000000000000000","amount":"1.00","nonce":1,"timestamp":9999999999,"signature":"0xdead"}`
	req := httptest.NewRequest("POST",
		"/v1/agents/0xwrongowner000000000000000000000000000000/sessions/"+key.ID+"/transact",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Transact_NoSpendScope(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	h := NewHandler(mgr, slog.Default())

	// Key with only "read" scope
	key, _ := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"read"},
	})

	body := `{"to":"0xaaaa000000000000000000000000000000000000","amount":"1.00","nonce":1,"timestamp":9999999999,"signature":"0xdead"}`
	req := httptest.NewRequest("POST",
		"/v1/agents/0x1234567890123456789012345678901234567890/sessions/"+key.ID+"/transact",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != ErrScopeNotAllowed.Code {
		t.Errorf("expected scope_not_allowed, got %v", resp["error"])
	}
}

func TestHandler_Transact_InvalidAddress(t *testing.T) {
	h, key := testHandlerWithKey(t)

	body := `{"to":"notanaddress","amount":"1.00","nonce":1,"timestamp":9999999999,"signature":"0xdead"}`
	req := httptest.NewRequest("POST",
		"/v1/agents/0x1234567890123456789012345678901234567890/sessions/"+key.ID+"/transact",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router := setupRouter(h)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Manager coverage tests ---

func TestManager_Get_NotFound(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Get(context.Background(), "nonexistent")
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestManager_List(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xBcdef01234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
	})

	keys, err := mgr.List(ctx, "0x1234567890123456789012345678901234567890")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestManager_CountActive(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
	})

	count, err := mgr.CountActive(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestManager_Revoke_Cascading(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
	})

	child := &SessionKey{
		ID:          "sk_child1",
		OwnerAddr:   "0x1234567890123456789012345678901234567890",
		PublicKey:   "0xchild01234567890123456789012345678901234",
		CreatedAt:   time.Now(),
		ParentKeyID: parent.ID,
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    []string{"spend", "read"},
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	store.Create(ctx, child)

	mgr.Revoke(ctx, parent.ID)

	childKey, _ := mgr.Get(ctx, child.ID)
	if childKey.RevokedAt == nil {
		t.Error("expected child to be revoked after parent revocation")
	}
}

func TestManager_RotateKey(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	newKey, err := mgr.RotateKey(ctx, key.ID, "0xnewkey1234567890123456789012345678901234")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newKey.RotatedFromID != key.ID {
		t.Errorf("expected rotatedFromID %s, got %s", key.ID, newKey.RotatedFromID)
	}
	if newKey.OwnerAddr != key.OwnerAddr {
		t.Errorf("expected same owner, got %s", newKey.OwnerAddr)
	}

	oldKey, _ := mgr.Get(ctx, key.ID)
	if oldKey.RotatedToID != newKey.ID {
		t.Errorf("expected rotatedToID %s, got %s", newKey.ID, oldKey.RotatedToID)
	}
}

func TestManager_RotateKey_NotFound(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.RotateKey(context.Background(), "nonexistent", "0xnewkey1234567890123456789012345678901234")
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestManager_RotateKey_Revoked(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})
	mgr.Revoke(ctx, key.ID)

	_, err := mgr.RotateKey(ctx, key.ID, "0xnewkey1234567890123456789012345678901234")
	if err != ErrKeyRevoked {
		t.Errorf("expected ErrKeyRevoked, got %v", err)
	}
}

func TestManager_RotateKey_AlreadyRotated(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	mgr.RotateKey(ctx, key.ID, "0xnewkey1234567890123456789012345678901234")
	_, err := mgr.RotateKey(ctx, key.ID, "0xanother234567890123456789012345678901234")
	if err != ErrKeyAlreadyRotated {
		t.Errorf("expected ErrKeyAlreadyRotated, got %v", err)
	}
}

func TestManager_RotateKey_InvalidPublicKey(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	_, err := mgr.RotateKey(ctx, key.ID, "invalid")
	if err != ErrInvalidPublicKey {
		t.Errorf("expected ErrInvalidPublicKey, got %v", err)
	}
}

func TestManager_ValidateScope(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	key, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"spend", "read"},
	})

	if err := mgr.ValidateScope(ctx, key.ID, "spend"); err != nil {
		t.Errorf("expected spend allowed: %v", err)
	}
	if err := mgr.ValidateScope(ctx, key.ID, "delegate"); err == nil {
		t.Error("expected delegate not allowed")
	}
}

func TestManager_PolicyStoreAccessor(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	if mgr.PolicyStore() != nil {
		t.Error("expected nil policy store")
	}
	if mgr.Store() != store {
		t.Error("expected matching store")
	}
}

func TestManager_AuditLogger(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	if mgr.AuditLogger() != nil {
		t.Error("expected nil audit logger")
	}
}

func TestManager_Create_InvalidPublicKey(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234", &SessionKeyRequest{
		PublicKey: "invalid",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err == nil {
		t.Error("expected error for invalid public key")
	}
}

func TestManager_Create_InvalidExpiresAt(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresAt: "not-a-date",
		AllowAny:  true,
	})
	if err == nil {
		t.Error("expected error for invalid expiresAt")
	}
}

func TestManager_Create_InvalidExpiresIn(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "invalid",
		AllowAny:  true,
	})
	if err == nil {
		t.Error("expected error for invalid expiresIn")
	}
}

func TestManager_Create_InvalidScope(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	_, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
		Scopes:    []string{"nonexistent_scope"},
	})
	if err != ErrInvalidScope {
		t.Errorf("expected ErrInvalidScope, got %v", err)
	}
}

func TestManager_Create_DefaultScopes(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	key, err := mgr.Create(context.Background(), "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0x1234567890123456789012345678901234567890",
		ExpiresIn: "1h",
		AllowAny:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(key.Permission.Scopes) != len(DefaultScopes) {
		t.Errorf("expected default scopes, got %v", key.Permission.Scopes)
	}
}

func TestManager_LockKey(t *testing.T) {
	mgr := NewManager(NewMemoryStore(), nil)
	unlock := mgr.LockKey("key1")
	unlock()
}

func TestManager_LockKeyChain(t *testing.T) {
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()

	parent, _ := mgr.Create(ctx, "0x1234567890123456789012345678901234567890", &SessionKeyRequest{
		PublicKey: "0xAbcdef1234567890123456789012345678901234",
		ExpiresIn: "1h",
		AllowAny:  true,
		MaxTotal:  "100.00",
	})

	child := &SessionKey{
		ID:          "sk_chain_child",
		OwnerAddr:   "0x1234567890123456789012345678901234567890",
		PublicKey:   "0xchild01234567890123456789012345678901234",
		CreatedAt:   time.Now(),
		ParentKeyID: parent.ID,
		Permission: Permission{
			ExpiresAt: time.Now().Add(time.Hour),
			AllowAny:  true,
			Scopes:    []string{"spend", "read"},
		},
		Usage: SessionKeyUsage{TotalSpent: "0", SpentToday: "0", LastResetDay: time.Now().Format("2006-01-02")},
	}
	store.Create(ctx, child)

	unlock := mgr.LockKeyChain(ctx, child.ID)
	unlock()
}

// --- SessionKey model tests ---

func TestSessionKey_HasScope(t *testing.T) {
	key := &SessionKey{Permission: Permission{Scopes: []string{"spend", "read"}}}
	if !key.HasScope("spend") {
		t.Error("expected spend scope")
	}
	if key.HasScope("delegate") {
		t.Error("expected no delegate scope")
	}
}

func TestSessionKey_HasScope_Default(t *testing.T) {
	key := &SessionKey{}
	if !key.HasScope("spend") {
		t.Error("expected default spend scope")
	}
	if !key.HasScope("read") {
		t.Error("expected default read scope")
	}
}

func TestSessionKey_IsActive_Variants(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	tests := []struct {
		name   string
		key    SessionKey
		active bool
	}{
		{"active", SessionKey{Permission: Permission{ExpiresAt: future}}, true},
		{"expired", SessionKey{Permission: Permission{ExpiresAt: past}}, false},
		{"revoked", SessionKey{Permission: Permission{ExpiresAt: future}, RevokedAt: &now}, false},
		{"not_yet_valid", SessionKey{Permission: Permission{ExpiresAt: future, ValidAfter: future}}, false},
		{"rotated_past_grace", SessionKey{
			Permission:       Permission{ExpiresAt: future},
			RotatedToID:      "other",
			RotationGraceEnd: &past,
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.key.IsActive() != tt.active {
				t.Errorf("expected IsActive=%v", tt.active)
			}
		})
	}
}

func TestValidationError(t *testing.T) {
	ve := &ValidationError{Code: "test", Message: "test error"}
	if ve.Error() != "test error" {
		t.Errorf("expected 'test error', got %s", ve.Error())
	}
}
