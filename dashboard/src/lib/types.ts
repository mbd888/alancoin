/** Backend API response types — mirrors Go structs */

export interface DashboardOverview {
  tenant: {
    id: string;
    name: string;
    plan: string;
  };
  billing: {
    totalRequests: number;
    settledRequests: number;
    settledVolume: string;
    feesCollected: string;
    takeRateBps: number;
  };
  activeSessions: number;
  agentCount: number;
}

export interface UsagePoint {
  period: string;
  requests: number;
  volume: string;
  fees: string;
}

export interface DashboardUsage {
  interval: string;
  from: string;
  to: string;
  points: UsagePoint[];
  count: number;
}

export interface TopService {
  serviceType: string;
  serviceName: string;
  requests: number;
  volume: string;
}

export interface DashboardDenial {
  id: string;
  policyId: string;
  policyName: string;
  reason: string;
  agentAddr: string;
  serviceType: string;
  amount: string;
  timestamp: string;
}

export interface GatewaySession {
  id: string;
  agentAddr: string;
  tenantId: string;
  maxTotal: string;
  maxPerRequest: string;
  totalSpent: string;
  requestCount: number;
  strategy: string;
  allowedTypes: string[];
  status: "active" | "exhausted" | "expired" | "settled";
  expiresAt: string;
  createdAt: string;
  updatedAt: string;
}

export interface Agent {
  address: string;
  name: string;
  description: string;
  ownerAddress: string;
  isAutonomous: boolean;
  endpoint: string;
  services: Service[];
  stats: AgentStats;
  createdAt: string;
  updatedAt: string;
}

export interface Service {
  id: string;
  type: string;
  name: string;
  price: string;
  description: string;
  endpoint: string;
  active: boolean;
}

export interface AgentStats {
  totalReceived: string;
  totalSent: string;
  transactionCount: number;
  successRate: number;
}

export interface PaginatedResponse {
  count: number;
  has_more: boolean;
  next_cursor?: string;
}

export type SessionsResponse = PaginatedResponse & {
  sessions: GatewaySession[];
};

export type AgentsResponse = { agents: Agent[] };
export type DenialsResponse = { denials: DashboardDenial[]; count: number };
export type TopServicesResponse = { services: TopService[]; count: number };

export interface Escrow {
  id: string;
  buyerAddr: string;
  sellerAddr: string;
  amount: string;
  serviceId: string;
  status: "pending" | "delivered" | "released" | "refunded" | "expired" | "disputed" | "arbitrating";
  autoReleaseAt: string;
  deliveredAt?: string;
  resolvedAt?: string;
  disputeReason?: string;
  createdAt: string;
  updatedAt: string;
}

export type EscrowsResponse = PaginatedResponse & {
  escrows: Escrow[];
};

export interface Workflow {
  id: string;
  buyerAddr: string;
  name: string;
  totalBudget: string;
  spentAmount: string;
  status: "open" | "completed" | "aborted";
  totalSteps: number;
  completedSteps: number;
  createdAt: string;
  updatedAt: string;
}

export type WorkflowsResponse = {
  workflows: Workflow[];
};

export interface StreamItem {
  id: string;
  buyerAddr: string;
  sellerAddr: string;
  holdAmount: string;
  spentAmount: string;
  pricePerTick: string;
  tickCount: number;
  status: "open" | "settled" | "closed";
  createdAt: string;
}

export type StreamsResponse = {
  streams: StreamItem[];
  count: number;
};

export interface OfferItem {
  id: string;
  sellerAddr: string;
  serviceType: string;
  description: string;
  price: string;
  capacity: number;
  remainingCap: number;
  status: "active" | "exhausted" | "cancelled" | "expired";
  expiresAt: string;
  createdAt: string;
}

export type OffersResponse = {
  offers: OfferItem[];
  count: number;
};

export interface RealtimeEvent {
  type: string;
  timestamp: string;
  data: Record<string, unknown>;
}

export interface SubsystemStatus {
  name: string;
  status: "up" | "down" | "degraded";
  detail: string;
}

export interface ReconciliationSnapshot {
  ledgerMismatches: number;
  stuckEscrows: number;
  staleStreams: number;
  orphanedHolds: number;
  invariantViolations: number;
  healthy: boolean;
  timestamp: string;
}

export interface SystemHealthResponse {
  status: string;
  services: SubsystemStatus[];
  reconciliation?: ReconciliationSnapshot;
}
