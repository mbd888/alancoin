package registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_AgentLifecycle(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create agent
	agent := &Agent{
		Address:     "0x1234567890123456789012345678901234567890",
		Name:        "TestAgent",
		Description: "A test agent",
	}

	err := store.CreateAgent(ctx, agent)
	require.NoError(t, err)

	// Get agent
	retrieved, err := store.GetAgent(ctx, agent.Address)
	require.NoError(t, err)
	assert.Equal(t, "TestAgent", retrieved.Name)
	assert.NotZero(t, retrieved.CreatedAt)

	// Try to create duplicate
	err = store.CreateAgent(ctx, agent)
	assert.ErrorIs(t, err, ErrAgentExists)

	// Update agent
	agent.Description = "Updated description"
	err = store.UpdateAgent(ctx, agent)
	require.NoError(t, err)

	retrieved, err = store.GetAgent(ctx, agent.Address)
	require.NoError(t, err)
	assert.Equal(t, "Updated description", retrieved.Description)

	// Delete agent
	err = store.DeleteAgent(ctx, agent.Address)
	require.NoError(t, err)

	// Verify deleted
	_, err = store.GetAgent(ctx, agent.Address)
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryStore_ServiceLifecycle(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create agent first
	agent := &Agent{
		Address: "0x1234567890123456789012345678901234567890",
		Name:    "TestAgent",
	}
	err := store.CreateAgent(ctx, agent)
	require.NoError(t, err)

	// Add service
	service := &Service{
		Type:        "inference",
		Name:        "GPT-4 API",
		Description: "Access to GPT-4",
		Price:       "0.001",
		Endpoint:    "https://api.example.com/inference",
	}

	err = store.AddService(ctx, agent.Address, service)
	require.NoError(t, err)
	assert.NotEmpty(t, service.ID)

	// Verify service was added
	retrieved, err := store.GetAgent(ctx, agent.Address)
	require.NoError(t, err)
	assert.Len(t, retrieved.Services, 1)
	assert.Equal(t, "GPT-4 API", retrieved.Services[0].Name)

	// Update service
	service.Price = "0.002"
	err = store.UpdateService(ctx, agent.Address, service)
	require.NoError(t, err)

	retrieved, err = store.GetAgent(ctx, agent.Address)
	require.NoError(t, err)
	assert.Equal(t, "0.002", retrieved.Services[0].Price)

	// Remove service
	err = store.RemoveService(ctx, agent.Address, service.ID)
	require.NoError(t, err)

	retrieved, err = store.GetAgent(ctx, agent.Address)
	require.NoError(t, err)
	assert.Len(t, retrieved.Services, 0)
}

func TestMemoryStore_ListServices(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create multiple agents with services
	agents := []struct {
		address string
		name    string
		service Service
	}{
		{
			address: "0x1111111111111111111111111111111111111111",
			name:    "Agent1",
			service: Service{Type: "inference", Name: "GPT-4", Price: "0.001"},
		},
		{
			address: "0x2222222222222222222222222222222222222222",
			name:    "Agent2",
			service: Service{Type: "inference", Name: "Claude", Price: "0.002"},
		},
		{
			address: "0x3333333333333333333333333333333333333333",
			name:    "Agent3",
			service: Service{Type: "translation", Name: "Translate", Price: "0.0005"},
		},
	}

	for _, a := range agents {
		agent := &Agent{Address: a.address, Name: a.name}
		err := store.CreateAgent(ctx, agent)
		require.NoError(t, err)
		err = store.AddService(ctx, a.address, &a.service)
		require.NoError(t, err)
	}

	// List all services
	services, err := store.ListServices(ctx, AgentQuery{})
	require.NoError(t, err)
	assert.Len(t, services, 3)

	// Filter by type
	services, err = store.ListServices(ctx, AgentQuery{ServiceType: "inference"})
	require.NoError(t, err)
	assert.Len(t, services, 2)

	// Filter by max price
	services, err = store.ListServices(ctx, AgentQuery{MaxPrice: "0.001"})
	require.NoError(t, err)
	assert.Len(t, services, 2) // 0.001 and 0.0005

	// Verify sorted by price
	assert.Equal(t, "0.0005", services[0].Price)
}

func TestMemoryStore_Transactions(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create agents
	agent1 := &Agent{Address: "0x1111111111111111111111111111111111111111", Name: "Agent1"}
	agent2 := &Agent{Address: "0x2222222222222222222222222222222222222222", Name: "Agent2"}
	require.NoError(t, store.CreateAgent(ctx, agent1))
	require.NoError(t, store.CreateAgent(ctx, agent2))

	// Record transaction
	tx := &Transaction{
		TxHash: "0xabcdef",
		From:   agent1.Address,
		To:     agent2.Address,
		Amount: "0.001",
		Status: "confirmed",
	}

	err := store.RecordTransaction(ctx, tx)
	require.NoError(t, err)
	assert.NotEmpty(t, tx.ID)

	// Get transaction
	retrieved, err := store.GetTransaction(ctx, tx.ID)
	require.NoError(t, err)
	assert.Equal(t, "0xabcdef", retrieved.TxHash)

	// List transactions for agent
	txs, err := store.ListTransactions(ctx, agent1.Address, 10)
	require.NoError(t, err)
	assert.Len(t, txs, 1)

	txs, err = store.ListTransactions(ctx, agent2.Address, 10)
	require.NoError(t, err)
	assert.Len(t, txs, 1)

	// Check network stats
	stats, err := store.GetNetworkStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.TotalAgents)
	assert.Equal(t, int64(1), stats.TotalTransactions)
	assert.Equal(t, "0.001000", stats.TotalVolume)
}

func TestMemoryStore_AddressNormalization(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create with mixed case
	agent := &Agent{
		Address: "0xAbCdEf1234567890123456789012345678901234",
		Name:    "TestAgent",
	}
	err := store.CreateAgent(ctx, agent)
	require.NoError(t, err)

	// Should be retrievable with any case
	_, err = store.GetAgent(ctx, "0xabcdef1234567890123456789012345678901234")
	require.NoError(t, err)

	_, err = store.GetAgent(ctx, "0xABCDEF1234567890123456789012345678901234")
	require.NoError(t, err)
}

func TestMemoryStore_DuplicateTxHash(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create agents
	agent1 := &Agent{Address: "0x1111111111111111111111111111111111111111", Name: "Agent1"}
	agent2 := &Agent{Address: "0x2222222222222222222222222222222222222222", Name: "Agent2"}
	require.NoError(t, store.CreateAgent(ctx, agent1))
	require.NoError(t, store.CreateAgent(ctx, agent2))

	// Record first transaction
	tx1 := &Transaction{
		TxHash: "0xdeadbeef",
		From:   agent1.Address,
		To:     agent2.Address,
		Amount: "1.00",
		Status: "confirmed",
	}
	err := store.RecordTransaction(ctx, tx1)
	require.NoError(t, err)

	// Record second transaction with same txHash — should fail
	tx2 := &Transaction{
		TxHash: "0xdeadbeef",
		From:   agent1.Address,
		To:     agent2.Address,
		Amount: "2.00",
		Status: "confirmed",
	}
	err = store.RecordTransaction(ctx, tx2)
	assert.Error(t, err, "duplicate txHash should be rejected")
	assert.Contains(t, err.Error(), "duplicate")

	// Empty txHash should be allowed multiple times (off-chain transactions)
	tx3 := &Transaction{
		From:   agent1.Address,
		To:     agent2.Address,
		Amount: "0.50",
		Status: "confirmed",
	}
	err = store.RecordTransaction(ctx, tx3)
	require.NoError(t, err)

	tx4 := &Transaction{
		From:   agent1.Address,
		To:     agent2.Address,
		Amount: "0.50",
		Status: "confirmed",
	}
	err = store.RecordTransaction(ctx, tx4)
	require.NoError(t, err)
}

func TestIsKnownServiceType(t *testing.T) {
	assert.True(t, IsKnownServiceType("inference"))
	assert.True(t, IsKnownServiceType("translation"))
	assert.True(t, IsKnownServiceType("code"))
	assert.False(t, IsKnownServiceType("unknown_type"))
	assert.False(t, IsKnownServiceType(""))
}

// --- ListAgents tests ---

func TestMemoryStore_ListAgents_Basic(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Empty store
	agents, err := store.ListAgents(ctx, AgentQuery{})
	require.NoError(t, err)
	assert.Empty(t, agents)

	// Add agents
	for i := 0; i < 5; i++ {
		addr := fmt.Sprintf("0x%040d", i)
		agent := &Agent{Address: addr, Name: fmt.Sprintf("Agent%d", i)}
		require.NoError(t, store.CreateAgent(ctx, agent))
	}

	agents, err = store.ListAgents(ctx, AgentQuery{})
	require.NoError(t, err)
	assert.Len(t, agents, 5)
}

func TestMemoryStore_ListAgents_Pagination(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		addr := fmt.Sprintf("0x%040d", i)
		agent := &Agent{Address: addr, Name: fmt.Sprintf("Agent%d", i)}
		require.NoError(t, store.CreateAgent(ctx, agent))
	}

	// Page 1
	agents, err := store.ListAgents(ctx, AgentQuery{Limit: 3, Offset: 0})
	require.NoError(t, err)
	assert.Len(t, agents, 3)

	// Page 2
	agents, err = store.ListAgents(ctx, AgentQuery{Limit: 3, Offset: 3})
	require.NoError(t, err)
	assert.Len(t, agents, 3)

	// Offset past end
	agents, err = store.ListAgents(ctx, AgentQuery{Limit: 10, Offset: 100})
	require.NoError(t, err)
	assert.Empty(t, agents)

	// Negative offset treated as 0
	agents, err = store.ListAgents(ctx, AgentQuery{Limit: 5, Offset: -5})
	require.NoError(t, err)
	assert.Len(t, agents, 5)
}

func TestMemoryStore_ListAgents_LimitCap(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Limit 0 defaults to 100, limit > 1000 capped to 1000
	agents, err := store.ListAgents(ctx, AgentQuery{Limit: 0})
	require.NoError(t, err)
	assert.Empty(t, agents) // no agents, just testing it doesn't panic

	agents, err = store.ListAgents(ctx, AgentQuery{Limit: 5000})
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func TestMemoryStore_ListAgents_FilterByServiceType(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr1 := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addr2 := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	addr3 := "0xcccccccccccccccccccccccccccccccccccccccc"

	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr1, Name: "Agent1"}))
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr2, Name: "Agent2"}))
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr3, Name: "Agent3"}))

	require.NoError(t, store.AddService(ctx, addr1, &Service{Type: "inference", Name: "GPT", Price: "0.01"}))
	require.NoError(t, store.AddService(ctx, addr2, &Service{Type: "translation", Name: "Trans", Price: "0.005"}))
	require.NoError(t, store.AddService(ctx, addr3, &Service{Type: "inference", Name: "Claude", Price: "0.02"}))

	// Filter by inference
	agents, err := store.ListAgents(ctx, AgentQuery{ServiceType: "inference"})
	require.NoError(t, err)
	assert.Len(t, agents, 2)

	// Filter by translation
	agents, err = store.ListAgents(ctx, AgentQuery{ServiceType: "translation"})
	require.NoError(t, err)
	assert.Len(t, agents, 1)

	// Filter by nonexistent type
	agents, err = store.ListAgents(ctx, AgentQuery{ServiceType: "nonexistent"})
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func TestMemoryStore_ListAgents_FilterByActiveService(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: "Agent1"}))

	svc := &Service{Type: "inference", Name: "GPT", Price: "0.01"}
	require.NoError(t, store.AddService(ctx, addr, svc))

	// Filter by service type + active=true should find it (services are active by default)
	active := true
	agents, err := store.ListAgents(ctx, AgentQuery{ServiceType: "inference", Active: &active})
	require.NoError(t, err)
	assert.Len(t, agents, 1)

	// Deactivate the service
	svc.Active = false
	require.NoError(t, store.UpdateService(ctx, addr, svc))

	// Now filter active=true should not find it
	agents, err = store.ListAgents(ctx, AgentQuery{ServiceType: "inference", Active: &active})
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func TestMemoryStore_ListAgents_SortedByTxCount(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr1 := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	addr2 := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	addr3 := "0xcccccccccccccccccccccccccccccccccccccccc"
	// external addresses (not registered) so receivers don't accumulate counts
	ext := "0xdddddddddddddddddddddddddddddddddddddddd"

	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr1, Name: "Low"}))
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr2, Name: "High"}))
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr3, Name: "Mid"}))

	// addr2 sends 5 txs to an external address -> addr2 gets 5 tx count
	for i := 0; i < 5; i++ {
		store.RecordTransaction(ctx, &Transaction{From: addr2, To: ext, Amount: "0.01", Status: "confirmed"})
	}
	// addr3 sends 2 txs -> addr3 gets 2 tx count
	for i := 0; i < 2; i++ {
		store.RecordTransaction(ctx, &Transaction{From: addr3, To: ext, Amount: "0.01", Status: "confirmed"})
	}
	// addr1 sends 1 tx -> addr1 gets 1 tx count
	store.RecordTransaction(ctx, &Transaction{From: addr1, To: ext, Amount: "0.01", Status: "confirmed"})

	agents, err := store.ListAgents(ctx, AgentQuery{})
	require.NoError(t, err)
	require.Len(t, agents, 3)
	// Sorted by tx count descending: High(5) > Mid(2) > Low(1)
	assert.Equal(t, "High", agents[0].Name)
	assert.Equal(t, "Mid", agents[1].Name)
	assert.Equal(t, "Low", agents[2].Name)
}

// --- GetRecentTransactions tests ---

func TestMemoryStore_GetRecentTransactions(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Empty
	txs, err := store.GetRecentTransactions(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, txs)

	// Add some transactions
	for i := 0; i < 5; i++ {
		tx := &Transaction{
			From:   fmt.Sprintf("0x%040d", i),
			To:     fmt.Sprintf("0x%040d", i+10),
			Amount: "1.00",
			Status: "confirmed",
		}
		require.NoError(t, store.RecordTransaction(ctx, tx))
	}

	// Get all
	txs, err = store.GetRecentTransactions(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, txs, 5)

	// Get limited
	txs, err = store.GetRecentTransactions(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, txs, 2)

	// Default limit (0 -> 50)
	txs, err = store.GetRecentTransactions(ctx, 0)
	require.NoError(t, err)
	assert.Len(t, txs, 5)
}

// --- UpdateAgentStats tests ---

func TestMemoryStore_UpdateAgentStats(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: "Agent1"}))

	// Update stats
	err := store.UpdateAgentStats(ctx, addr, func(stats *AgentStats) {
		stats.TransactionCount = 42
		stats.SuccessRate = 0.95
	})
	require.NoError(t, err)

	// Verify
	agent, err := store.GetAgent(ctx, addr)
	require.NoError(t, err)
	assert.Equal(t, int64(42), agent.Stats.TransactionCount)
	assert.Equal(t, 0.95, agent.Stats.SuccessRate)
}

func TestMemoryStore_UpdateAgentStats_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.UpdateAgentStats(ctx, "0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead", func(stats *AgentStats) {
		stats.TransactionCount = 1
	})
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryStore_UpdateAgentStats_CaseInsensitive(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xAbCdEf1234567890123456789012345678901234"
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: "CaseAgent"}))

	err := store.UpdateAgentStats(ctx, "0xABCDEF1234567890123456789012345678901234", func(stats *AgentStats) {
		stats.TransactionCount = 10
	})
	require.NoError(t, err)
}

// --- Additional edge cases for existing methods ---

func TestMemoryStore_UpdateAgent_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.UpdateAgent(ctx, &Agent{Address: "0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead"})
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryStore_DeleteAgent_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.DeleteAgent(ctx, "0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead")
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryStore_AddService_AgentNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.AddService(ctx, "0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead", &Service{Type: "code", Name: "Test", Price: "1.00"})
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryStore_UpdateService_AgentNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.UpdateService(ctx, "0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead", &Service{ID: "svc_123"})
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryStore_UpdateService_ServiceNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: "Agent1"}))

	err := store.UpdateService(ctx, addr, &Service{ID: "svc_nonexistent"})
	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestMemoryStore_RemoveService_AgentNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.RemoveService(ctx, "0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead", "svc_123")
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryStore_RemoveService_ServiceNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: "Agent1"}))

	err := store.RemoveService(ctx, addr, "svc_nonexistent")
	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestMemoryStore_GetTransaction_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.GetTransaction(ctx, "nonexistent_id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMemoryStore_ListTransactions_DefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// limit=0 defaults to 100
	txs, err := store.ListTransactions(ctx, addr, 0)
	require.NoError(t, err)
	assert.Empty(t, txs)
}

func TestMemoryStore_ListServices_MinPrice(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: "Agent1"}))
	require.NoError(t, store.AddService(ctx, addr, &Service{Type: "inference", Name: "Cheap", Price: "0.001"}))
	require.NoError(t, store.AddService(ctx, addr, &Service{Type: "inference", Name: "Expensive", Price: "1.000"}))

	// Min price filter
	services, err := store.ListServices(ctx, AgentQuery{MinPrice: "0.5"})
	require.NoError(t, err)
	assert.Len(t, services, 1)
	assert.Equal(t, "Expensive", services[0].Name)
}

func TestMemoryStore_ListServices_Pagination(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		addr := fmt.Sprintf("0x%040d", i)
		require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: fmt.Sprintf("Agent%d", i)}))
		require.NoError(t, store.AddService(ctx, addr, &Service{
			Type:  "inference",
			Name:  fmt.Sprintf("Service%d", i),
			Price: fmt.Sprintf("0.%03d", i+1),
		}))
	}

	// First page
	services, err := store.ListServices(ctx, AgentQuery{Limit: 2, Offset: 0})
	require.NoError(t, err)
	assert.Len(t, services, 2)

	// Second page
	services, err = store.ListServices(ctx, AgentQuery{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, services, 2)

	// Past end
	services, err = store.ListServices(ctx, AgentQuery{Limit: 10, Offset: 100})
	require.NoError(t, err)
	assert.Empty(t, services)
}

func TestMemoryStore_ListServices_ActiveFilter(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, store.CreateAgent(ctx, &Agent{Address: addr, Name: "Agent1"}))

	svc := &Service{Type: "inference", Name: "GPT", Price: "0.01"}
	require.NoError(t, store.AddService(ctx, addr, svc))

	// Deactivate the service
	svc.Active = false
	require.NoError(t, store.UpdateService(ctx, addr, svc))

	// Active=true should not return the inactive service
	active := true
	services, err := store.ListServices(ctx, AgentQuery{Active: &active})
	require.NoError(t, err)
	assert.Empty(t, services)

	// Active=false should return it
	inactive := false
	services, err = store.ListServices(ctx, AgentQuery{Active: &inactive})
	require.NoError(t, err)
	assert.Len(t, services, 1)
}

func TestMemoryStore_RecordTransaction_PendingDoesNotUpdateVolume(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Record a pending transaction - should NOT update volume
	tx := &Transaction{
		From:   "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		To:     "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Amount: "10.00",
		Status: "pending",
	}
	require.NoError(t, store.RecordTransaction(ctx, tx))

	stats, err := store.GetNetworkStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, "0", stats.TotalVolume)
	assert.Equal(t, int64(1), stats.TotalTransactions) // counted but volume not added
}

// --- Partition helper tests ---

func TestQuoteIdentifier(t *testing.T) {
	assert.Equal(t, `"transactions_2024_01"`, quoteIdentifier("transactions_2024_01"))
	assert.Equal(t, `"has""quote"`, quoteIdentifier(`has"quote`))
}

// Benchmark

func BenchmarkMemoryStore_CreateAgent(b *testing.B) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		agent := &Agent{
			Address: "0x" + string(rune('0'+i%10)) + "234567890123456789012345678901234567890",
			Name:    "Agent",
		}
		store.CreateAgent(ctx, agent)
	}
}

func BenchmarkMemoryStore_ListServices(b *testing.B) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Setup: create 100 agents with services
	for i := 0; i < 100; i++ {
		agent := &Agent{
			Address: "0x" + string(rune('a'+i%26)) + "234567890123456789012345678901234567890",
			Name:    "Agent",
		}
		store.CreateAgent(ctx, agent)
		store.AddService(ctx, agent.Address, &Service{
			Type:  "inference",
			Price: "0.001",
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.ListServices(ctx, AgentQuery{ServiceType: "inference"})
	}
}
