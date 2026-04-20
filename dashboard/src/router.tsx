import { useEffect } from "react";
import {
  createRouter,
  createRootRoute,
  createRoute,
  lazyRouteComponent,
  Outlet,
  redirect,
  Link,
  useRouterState,
} from "@tanstack/react-router";
import { AppShell } from "@/components/layouts/app-shell";
import { AuthGate } from "@/components/auth/auth-gate";
import { useAuthStore } from "@/stores/auth-store";

function RouteProgress() {
  const isLoading = useRouterState({ select: (s) => s.isLoading });
  if (!isLoading) return null;
  return (
    <div className="fixed inset-x-0 top-0 z-50 h-0.5 overflow-hidden bg-muted">
      <div className="h-full w-1/3 animate-pulse bg-accent-foreground" style={{ animation: "progress 1.2s ease-in-out infinite" }} />
    </div>
  );
}

function RootComponent() {
  const { isAuthenticated, isValidating, restore } = useAuthStore();

  useEffect(() => {
    restore();
  }, [restore]);

  if (isValidating) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--background)]">
        <div className="size-6 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
      </div>
    );
  }

  if (!isAuthenticated) {
    return <AuthGate />;
  }

  return (
    <AppShell>
      <RouteProgress />
      <Outlet />
    </AppShell>
  );
}

const rootRoute = createRootRoute({
  component: RootComponent,
  notFoundComponent: () => (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-4 px-4 text-center">
      <h1 className="text-4xl font-bold tabular-nums text-foreground">404</h1>
      <p className="text-sm text-muted-foreground">This page does not exist.</p>
      <Link to="/overview" className="text-sm text-accent-foreground underline underline-offset-4 hover:text-foreground">
        Go to Overview
      </Link>
    </div>
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
  component: lazyRouteComponent(() => import("@/routes/dashboard/overview"), "OverviewPage"),
});

const sessionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sessions",
  component: lazyRouteComponent(() => import("@/routes/dashboard/sessions"), "SessionsPage"),
});

const agentsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/agents",
  component: lazyRouteComponent(() => import("@/routes/dashboard/agents"), "AgentsPage"),
});

const alertsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/alerts",
  component: lazyRouteComponent(() => import("@/routes/dashboard/alerts"), "AlertsPage"),
});

const chargebackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/chargeback",
  component: lazyRouteComponent(() => import("@/routes/dashboard/chargeback"), "ChargebackPage"),
});

const certificatesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/certificates",
  component: lazyRouteComponent(() => import("@/routes/dashboard/certificates"), "CertificatesPage"),
});

const intelligenceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/intelligence",
  component: lazyRouteComponent(() => import("@/routes/dashboard/intelligence"), "IntelligencePage"),
});

const healthRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/health",
  component: lazyRouteComponent(() => import("@/routes/dashboard/health"), "HealthPage"),
});

const liveFeedRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/live-feed",
  component: lazyRouteComponent(() => import("@/routes/dashboard/live-feed"), "LiveFeedPage"),
});

const escrowRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/escrow",
  component: lazyRouteComponent(() => import("@/routes/dashboard/escrow"), "EscrowPage"),
});

const budgetRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/budget",
  component: lazyRouteComponent(() => import("@/routes/dashboard/budget"), "BudgetPage"),
});

const workflowsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workflows",
  component: lazyRouteComponent(() => import("@/routes/dashboard/workflows"), "WorkflowsPage"),
});

const streamsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/streams",
  component: lazyRouteComponent(() => import("@/routes/dashboard/streams"), "StreamsPage"),
});

const marketplaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/marketplace",
  component: lazyRouteComponent(() => import("@/routes/dashboard/marketplace"), "MarketplacePage"),
});

const apiKeysRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/api-keys",
  component: lazyRouteComponent(() => import("@/routes/settings/api-keys"), "ApiKeysPage"),
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings",
  component: lazyRouteComponent(() => import("@/routes/settings/general"), "SettingsPage"),
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
