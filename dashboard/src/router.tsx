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
  alertsRoute,
  chargebackRoute,
  certificatesRoute,
  apiKeysRoute,
  settingsRoute,
]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
