import {
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
  redirect,
} from "@tanstack/react-router";
import { AppShell } from "@/components/layouts/app-shell";
import { OverviewPage } from "@/routes/dashboard/overview";
import { SessionsPage } from "@/routes/dashboard/sessions";
import { AgentsPage } from "@/routes/dashboard/agents";
import { AlertsPage } from "@/routes/dashboard/alerts";
import { ChargebackPage } from "@/routes/dashboard/chargeback";
import { CertificatesPage } from "@/routes/dashboard/certificates";
import { IntelligencePage } from "@/routes/dashboard/intelligence";
import { HealthPage } from "@/routes/dashboard/health";
import { LiveFeedPage } from "@/routes/dashboard/live-feed";
import { EscrowPage } from "@/routes/dashboard/escrow";
import { BudgetPage } from "@/routes/dashboard/budget";
import { WorkflowsPage } from "@/routes/dashboard/workflows";
import { StreamsPage } from "@/routes/dashboard/streams";
import { MarketplacePage } from "@/routes/dashboard/marketplace";
import { ApiKeysPage } from "@/routes/settings/api-keys";
import { SettingsPage } from "@/routes/settings/general";

const rootRoute = createRootRoute({
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/overview" });
  },
});

const overviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/overview",
  component: OverviewPage,
});

const sessionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sessions",
  component: SessionsPage,
});

const agentsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/agents",
  component: AgentsPage,
});

const alertsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/alerts",
  component: AlertsPage,
});

const chargebackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/chargeback",
  component: ChargebackPage,
});

const certificatesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/certificates",
  component: CertificatesPage,
});

const intelligenceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/intelligence",
  component: IntelligencePage,
});

const healthRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/health",
  component: HealthPage,
});

const liveFeedRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/live-feed",
  component: LiveFeedPage,
});

const escrowRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/escrow",
  component: EscrowPage,
});

const budgetRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/budget",
  component: BudgetPage,
});

const workflowsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workflows",
  component: WorkflowsPage,
});

const streamsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/streams",
  component: StreamsPage,
});

const marketplaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/marketplace",
  component: MarketplacePage,
});

const apiKeysRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/api-keys",
  component: ApiKeysPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings",
  component: SettingsPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  overviewRoute,
  sessionsRoute,
  agentsRoute,
  liveFeedRoute,
  escrowRoute,
  budgetRoute,
  workflowsRoute,
  streamsRoute,
  marketplaceRoute,
  alertsRoute,
  chargebackRoute,
  certificatesRoute,
  intelligenceRoute,
  healthRoute,
  apiKeysRoute,
  settingsRoute,
]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
