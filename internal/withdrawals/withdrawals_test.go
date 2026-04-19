package withdrawals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

const (
	testAgent  = "0x1111111111111111111111111111111111111111"
	testToAddr = "0x2222222222222222222222222222222222222222"
)

// --- stub ledger ---

type stubLedger struct {
	mu sync.Mutex

	holds    []ledgerCall
	confirms []ledgerCall
	releases []ledgerCall

	holdErr    error
	confirmErr error
	releaseErr error
}

type ledgerCall struct {
	Agent, Amount, Ref string
}

func (l *stubLedger) Hold(_ context.Context, agent, amount, ref string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holdErr != nil {
		return l.holdErr
	}
	l.holds = append(l.holds, ledgerCall{agent, amount, ref})
	return nil
}

func (l *stubLedger) ConfirmHold(_ context.Context, agent, amount, ref string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.confirmErr != nil {
		return l.confirmErr
	}
	l.confirms = append(l.confirms, ledgerCall{agent, amount, ref})
	return nil
}

func (l *stubLedger) ReleaseHold(_ context.Context, agent, amount, ref string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.releaseErr != nil {
		return l.releaseErr
	}
	l.releases = append(l.releases, ledgerCall{agent, amount, ref})
	return nil
}

func (l *stubLedger) counts() (int, int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.holds), len(l.confirms), len(l.releases)
}

// --- stub payouts ---

type stubPayouts struct {
	mu sync.Mutex

	calls  int
	result Result
	err    error
}

func (p *stubPayouts) Send(_ context.Context, to, amount, ref string) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	_ = to
	_ = amount
	_ = ref
	return p.result, p.err
}

func newService(t *testing.T) (*Service, *stubLedger, *stubPayouts) {
	t.Helper()
	l := &stubLedger{}
	p := &stubPayouts{result: Result{Status: "success", TxHash: "0xabc"}}
	svc, err := NewService(l, p, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, l, p
}

// --- service tests ---

func TestWithdraw_SuccessConfirmsHold(t *testing.T) {
	svc, ledger, payouts := newService(t)
	w, err := svc.Withdraw(context.Background(), Request{
		AgentAddr: testAgent, ToAddr: testToAddr,
		Amount: "1.500000", ClientRef: "ref-1",
	})
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if w.Status != "success" {
		t.Errorf("status=%s want success", w.Status)
	}
	holds, confirms, releases := ledger.counts()
	if holds != 1 || confirms != 1 || releases != 0 {
		t.Errorf("ledger calls: hold=%d confirm=%d release=%d", holds, confirms, releases)
	}
	if payouts.calls != 1 {
		t.Errorf("payouts.calls=%d want 1", payouts.calls)
	}
	if w.AgentAddr != strings.ToLower(testAgent) || w.ToAddr != strings.ToLower(testToAddr) {
		t.Errorf("addresses not lowercased: from=%s to=%s", w.AgentAddr, w.ToAddr)
	}
	// Ledger reference should be prefixed so audit trails can filter.
	if ref := ledger.holds[0].Ref; ref != "withdraw:ref-1" {
		t.Errorf("hold ref=%q want withdraw:ref-1", ref)
	}
}

func TestWithdraw_OnChainFailureReleasesHold(t *testing.T) {
	svc, ledger, payouts := newService(t)
	payouts.result = Result{Status: "failed", TxHash: "0xbad"}

	w, err := svc.Withdraw(context.Background(), Request{
		AgentAddr: testAgent, ToAddr: testToAddr,
		Amount: "0.500000", ClientRef: "ref-fail",
	})
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if w.Status != "failed" {
		t.Errorf("status=%s want failed", w.Status)
	}
	holds, confirms, releases := ledger.counts()
	if holds != 1 || confirms != 0 || releases != 1 {
		t.Errorf("ledger calls: hold=%d confirm=%d release=%d", holds, confirms, releases)
	}
}

func TestWithdraw_DroppedTreatedAsFailure(t *testing.T) {
	svc, ledger, payouts := newService(t)
	payouts.result = Result{Status: "dropped", TxHash: "0xgone"}

	_, err := svc.Withdraw(context.Background(), Request{
		AgentAddr: testAgent, ToAddr: testToAddr,
		Amount: "0.100000", ClientRef: "ref-drop",
	})
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	_, confirms, releases := ledger.counts()
	if confirms != 0 || releases != 1 {
		t.Errorf("dropped should release hold; got confirms=%d releases=%d", confirms, releases)
	}
}

func TestWithdraw_PayoutErrorReleasesHold(t *testing.T) {
	svc, ledger, payouts := newService(t)
	payouts.err = errors.New("rpc down")

	_, err := svc.Withdraw(context.Background(), Request{
		AgentAddr: testAgent, ToAddr: testToAddr,
		Amount: "1.000000", ClientRef: "ref-err",
	})
	if !errors.Is(err, ErrPayoutFailed) {
		t.Errorf("expected ErrPayoutFailed, got %v", err)
	}
	_, confirms, releases := ledger.counts()
	if confirms != 0 || releases != 1 {
		t.Errorf("payout error should release hold; got confirms=%d releases=%d", confirms, releases)
	}
}

func TestWithdraw_LedgerHoldFailureIsEarlyReturn(t *testing.T) {
	svc, ledger, payouts := newService(t)
	ledger.holdErr = errors.New("insufficient balance")

	_, err := svc.Withdraw(context.Background(), Request{
		AgentAddr: testAgent, ToAddr: testToAddr,
		Amount: "100.000000", ClientRef: "ref-poor",
	})
	if !errors.Is(err, ErrLedgerHold) {
		t.Errorf("expected ErrLedgerHold, got %v", err)
	}
	if payouts.calls != 0 {
		t.Errorf("payouts should not be called when hold fails; got %d", payouts.calls)
	}
}

func TestWithdraw_ConfirmFailureAfterOnChainSuccess(t *testing.T) {
	svc, ledger, _ := newService(t)
	ledger.confirmErr = errors.New("db down")

	// Confirm failing AFTER on-chain success is the nasty case: funds have
	// moved but the ledger didn't debit. Service must NOT release the hold
	// (that would double-credit). Error must surface.
	_, err := svc.Withdraw(context.Background(), Request{
		AgentAddr: testAgent, ToAddr: testToAddr,
		Amount: "1.000000", ClientRef: "ref-ledger-broken",
	})
	if err == nil {
		t.Fatal("expected error when confirm fails")
	}
	_, confirms, releases := ledger.counts()
	if confirms != 0 {
		t.Errorf("confirm should not have succeeded")
	}
	if releases != 0 {
		t.Fatalf("CRITICAL: release was called after on-chain success; double-credit risk (releases=%d)", releases)
	}
}

func TestWithdraw_PayoutErrorAndReleaseBothFail(t *testing.T) {
	svc, ledger, payouts := newService(t)
	payouts.err = errors.New("rpc down")
	ledger.releaseErr = errors.New("db down")

	_, err := svc.Withdraw(context.Background(), Request{
		AgentAddr: testAgent, ToAddr: testToAddr,
		Amount: "1.000000", ClientRef: "ref-both-fail",
	})
	if err == nil {
		t.Fatal("expected compound error")
	}
	if !strings.Contains(err.Error(), "release also failed") {
		t.Errorf("expected compound message, got %v", err)
	}
}

func TestWithdraw_ValidationErrors(t *testing.T) {
	svc, ledger, payouts := newService(t)
	cases := []struct {
		name string
		req  Request
		want error
	}{
		{"empty ref", Request{AgentAddr: testAgent, ToAddr: testToAddr, Amount: "1", ClientRef: ""}, ErrMissingRef},
		{"bad agent", Request{AgentAddr: "nope", ToAddr: testToAddr, Amount: "1", ClientRef: "r"}, ErrBadAgent},
		{"bad to", Request{AgentAddr: testAgent, ToAddr: "nope", Amount: "1", ClientRef: "r"}, ErrBadRecipient},
		{"zero amount", Request{AgentAddr: testAgent, ToAddr: testToAddr, Amount: "0", ClientRef: "r"}, ErrBadAmount},
		{"negative amount", Request{AgentAddr: testAgent, ToAddr: testToAddr, Amount: "-1", ClientRef: "r"}, ErrBadAmount},
		{"empty amount", Request{AgentAddr: testAgent, ToAddr: testToAddr, Amount: "", ClientRef: "r"}, ErrBadAmount},
		{"letters", Request{AgentAddr: testAgent, ToAddr: testToAddr, Amount: "abc", ClientRef: "r"}, ErrBadAmount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Withdraw(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Errorf("want %v got %v", tc.want, err)
			}
		})
	}
	if h, _, _ := ledger.counts(); h != 0 {
		t.Errorf("bad requests must not reach the ledger; holds=%d", h)
	}
	if payouts.calls != 0 {
		t.Errorf("bad requests must not reach payouts; calls=%d", payouts.calls)
	}
}

// --- handler tests ---

func newTestRouter(t *testing.T, svc *Service) *gin.Engine {
	t.Helper()
	r := gin.New()
	g := r.Group("/v1")
	NewHandler(svc).RegisterRoutes(g)
	return r
}

func postJSON(t *testing.T, r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestHandler_HappyPath(t *testing.T) {
	svc, _, _ := newService(t)
	r := newTestRouter(t, svc)

	body := map[string]string{
		"to":        testToAddr,
		"amount":    "1.000000",
		"clientRef": "ref-http-1",
	}
	w := postJSON(t, r, "/v1/agents/"+testAgent+"/payouts", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Withdrawal Withdrawal `json:"withdrawal"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Withdrawal.Status != "success" {
		t.Errorf("status=%s", resp.Withdrawal.Status)
	}
}

func TestHandler_DisabledReturns503(t *testing.T) {
	r := newTestRouter(t, nil) // nil svc → register stubs
	body := map[string]string{"to": testToAddr, "amount": "1", "clientRef": "r"}
	w := postJSON(t, r, "/v1/agents/"+testAgent+"/payouts", body)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d want 503", w.Code)
	}
}

func TestHandler_ValidationReturns400(t *testing.T) {
	svc, _, _ := newService(t)
	r := newTestRouter(t, svc)

	body := map[string]string{"to": testToAddr, "amount": "-5", "clientRef": "r"}
	w := postJSON(t, r, "/v1/agents/"+testAgent+"/payouts", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400 body=%s", w.Code, w.Body.String())
	}
}

func TestHandler_PayoutFailureReturns502(t *testing.T) {
	l := &stubLedger{}
	p := &stubPayouts{err: errors.New("rpc down")}
	svc, _ := NewService(l, p, nil)
	r := newTestRouter(t, svc)

	body := map[string]string{"to": testToAddr, "amount": "1", "clientRef": "r"}
	w := postJSON(t, r, "/v1/agents/"+testAgent+"/payouts", body)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status=%d want 502 body=%s", w.Code, w.Body.String())
	}
}

func TestHandler_LedgerHoldReturns409(t *testing.T) {
	l := &stubLedger{holdErr: errors.New("insufficient balance")}
	p := &stubPayouts{}
	svc, _ := NewService(l, p, nil)
	r := newTestRouter(t, svc)

	body := map[string]string{"to": testToAddr, "amount": "1000", "clientRef": "r"}
	w := postJSON(t, r, "/v1/agents/"+testAgent+"/payouts", body)
	if w.Code != http.StatusConflict {
		t.Errorf("status=%d want 409 body=%s", w.Code, w.Body.String())
	}
}

// sanity: ensure Result.FinalizedAt survives round-trip via handler
func TestHandler_FinalizedAtInResponse(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	l := &stubLedger{}
	p := &stubPayouts{result: Result{
		Status:      "success",
		TxHash:      "0xabc",
		SubmittedAt: now,
		FinalizedAt: &now,
	}}
	svc, _ := NewService(l, p, nil)
	r := newTestRouter(t, svc)

	body := map[string]string{"to": testToAddr, "amount": "1", "clientRef": "r"}
	w := postJSON(t, r, "/v1/agents/"+testAgent+"/payouts", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(fmt.Sprintf("%d", now.Year()))) {
		t.Errorf("response missing finalizedAt: %s", w.Body.String())
	}
}
