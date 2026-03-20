import { useState } from "react";
import { Globe, Webhook, CreditCard, Shield, ExternalLink } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";

export function SettingsPage() {
  const [tenantName, setTenantName] = useState("Default Tenant");

  return (
    <div className="min-h-screen">
      <header className="border-b border-[var(--border)] px-8 py-5">
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Settings</h1>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Manage tenant configuration
        </p>
      </header>

      {/* Narrow centered column — settings layout pattern */}
      <div className="mx-auto max-w-2xl px-8 py-8">
        {/* General section */}
        <SettingsSection
          icon={Globe}
          title="General"
          description="Basic tenant information and preferences."
        >
          <div className="flex items-start justify-between gap-8">
            <div className="flex-1">
              <Input
                id="tenant-name"
                label="Tenant Name"
                value={tenantName}
                onChange={(e) => setTenantName(e.target.value)}
              />
            </div>
            <Button
              variant="primary"
              size="sm"
              className="mt-6"
              onClick={() => toast.success("Settings saved")}
            >
              Save
            </Button>
          </div>
          <SettingsRow label="Plan" description="Current subscription tier">
            <Badge variant="accent">Pro</Badge>
          </SettingsRow>
          <SettingsRow label="Take Rate" description="Fee applied to settled transactions">
            <span className="tabular-nums text-[13px] text-[var(--foreground-secondary)]">
              250 bps (2.5%)
            </span>
          </SettingsRow>
          <SettingsRow label="Tenant ID" description="Use this in API calls">
            <code className="rounded-[var(--radius-sm)] bg-[var(--background-interactive)] px-2 py-1 font-mono text-[12px] text-[var(--foreground-secondary)]">
              ten_default
            </code>
          </SettingsRow>
        </SettingsSection>

        <Divider />

        {/* Webhooks section */}
        <SettingsSection
          icon={Webhook}
          title="Webhooks"
          description="Get notified when events happen in your account."
        >
          <div className="rounded-[var(--radius-lg)] border border-dashed border-[var(--border)] bg-[var(--background)] px-4 py-6 text-center">
            <Webhook size={20} strokeWidth={1.5} className="mx-auto text-[var(--foreground-disabled)]" />
            <p className="mt-2 text-[13px] text-[var(--foreground-muted)]">
              No webhooks configured
            </p>
            <p className="mt-1 text-[12px] text-[var(--foreground-disabled)]">
              Receive real-time notifications for session events, settlements, and policy denials.
            </p>
            <Button variant="secondary" size="sm" className="mt-4">
              Add Webhook Endpoint
            </Button>
          </div>
        </SettingsSection>

        <Divider />

        {/* Billing section */}
        <SettingsSection
          icon={CreditCard}
          title="Billing"
          description="Manage your subscription and payment method."
        >
          <SettingsRow label="Current Plan" description="Your active subscription">
            <div className="flex items-center gap-2">
              <Badge variant="accent">Pro</Badge>
              <span className="text-[12px] text-[var(--foreground-muted)]">$49/mo</span>
            </div>
          </SettingsRow>
          <SettingsRow label="Payment Method" description="Card on file">
            <span className="text-[13px] text-[var(--foreground-secondary)]">
              &bull;&bull;&bull;&bull; 4242
            </span>
          </SettingsRow>
          <div className="flex gap-2">
            <Button variant="secondary" size="sm">
              <ExternalLink size={13} />
              Manage in Stripe
            </Button>
            <Button variant="ghost" size="sm">
              View Invoices
            </Button>
          </div>
        </SettingsSection>

        <Divider />

        {/* Security */}
        <SettingsSection
          icon={Shield}
          title="Security"
          description="Authentication and access control settings."
        >
          <SettingsRow label="CORS Origins" description="Allowed origins for browser API calls">
            <code className="rounded-[var(--radius-sm)] bg-[var(--background-interactive)] px-2 py-1 font-mono text-[12px] text-[var(--foreground-secondary)]">
              *
            </code>
          </SettingsRow>
          <SettingsRow
            label="Rate Limit"
            description="Max requests per minute per API key"
          >
            <span className="tabular-nums text-[13px] text-[var(--foreground-secondary)]">
              1,000 req/min
            </span>
          </SettingsRow>
        </SettingsSection>

        <Divider />

        {/* Danger zone */}
        <section>
          <h2 className="text-[14px] font-medium text-[var(--color-danger)]">Danger Zone</h2>
          <p className="mt-1 text-[12px] text-[var(--foreground-muted)]">
            Irreversible actions. Proceed with caution.
          </p>
          <div className="mt-4 rounded-[var(--radius-lg)] border border-[oklch(0.30_0.06_25)] bg-[oklch(0.15_0.02_25)] p-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-[13px] font-medium text-[var(--foreground)]">
                  Delete tenant
                </p>
                <p className="mt-0.5 text-[12px] text-[var(--foreground-muted)]">
                  Permanently delete this tenant and all associated data.
                </p>
              </div>
              <Button variant="danger" size="sm">
                Delete Tenant
              </Button>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}

function SettingsSection({
  icon: Icon,
  title,
  description,
  children,
}: {
  icon: typeof Globe;
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <section>
      <div className="flex items-center gap-2">
        <Icon size={15} strokeWidth={1.8} className="text-[var(--foreground-muted)]" />
        <h2 className="text-[14px] font-medium text-[var(--foreground)]">{title}</h2>
      </div>
      <p className="mt-1 text-[12px] text-[var(--foreground-muted)]">{description}</p>
      <div className="mt-5 flex flex-col gap-5">{children}</div>
    </section>
  );
}

function SettingsRow({
  label,
  description,
  children,
}: {
  label: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-8">
      <div>
        <span className="text-[13px] font-medium text-[var(--foreground)]">{label}</span>
        <p className="mt-0.5 text-[12px] text-[var(--foreground-muted)]">{description}</p>
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

function Divider() {
  return <hr className="my-8 border-[var(--border-subtle)]" />;
}
