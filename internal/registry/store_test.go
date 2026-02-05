package registry

import (
	"context"
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

func TestIsKnownServiceType(t *testing.T) {
	assert.True(t, IsKnownServiceType("inference"))
	assert.True(t, IsKnownServiceType("translation"))
	assert.True(t, IsKnownServiceType("code"))
	assert.False(t, IsKnownServiceType("unknown_type"))
	assert.False(t, IsKnownServiceType(""))
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
