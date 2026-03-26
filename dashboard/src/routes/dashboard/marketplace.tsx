import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { useOffers } from "@/hooks/api/use-offers";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { OfferItem } from "@/lib/types";
import { Search } from "lucide-react";

const STATUS_VARIANT: Record<string, string> = {
  active: "success",
  exhausted: "warning",
  cancelled: "default",
  expired: "default",
};

export function MarketplacePage() {
  const [serviceFilter, setServiceFilter] = useState("");
  const offers = useOffers(serviceFilter);

  const allOffers = offers.data?.offers ?? [];

  const columns: Column<OfferItem>[] = [
    {
      id: "id",
      header: "Offer",
      cell: (row) => (
        <span className="font-mono text-[12px]">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "seller",
      header: "Seller",
      cell: (row) => (
        <span className="font-mono text-[12px]">
          {row.sellerAddr.slice(0, 8)}...{row.sellerAddr.slice(-4)}
        </span>
      ),
    },
    {
      id: "type",
      header: "Service",
      cell: (row) => (
        <Badge variant="accent">{row.serviceType}</Badge>
      ),
    },
    {
      id: "price",
      header: "Price",
      numeric: true,
      cell: (row) => formatCurrency(row.price),
    },
    {
      id: "capacity",
      header: "Capacity",
      cell: (row) => (
        <span className="text-[12px]">
          {row.remainingCap}
          <span className="text-[var(--foreground-disabled)]"> / </span>
          {row.capacity}
        </span>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (row) => (
        <Badge variant={(STATUS_VARIANT[row.status] ?? "default") as "success" | "warning" | "default"}>
          {row.status}
        </Badge>
      ),
    },
    {
      id: "expires",
      header: "Expires",
      cell: (row) => (
        <span className="text-[12px] text-[var(--foreground-muted)]">
          {relativeTime(row.expiresAt)}
        </span>
      ),
    },
    {
      id: "created",
      header: "Created",
      cell: (row) => (
        <span className="text-[12px] text-[var(--foreground-muted)]">
          {relativeTime(row.createdAt)}
        </span>
      ),
    },
  ];

  return (
    <div className="min-h-screen">
      <header className="border-b border-[var(--border)] px-8 py-5">
        <h1 className="text-[16px] font-semibold text-[var(--foreground)]">Marketplace</h1>
        <p className="mt-0.5 text-[13px] text-[var(--foreground-muted)]">
          Standing offers from service providers
        </p>
      </header>

      {/* Service type filter */}
      <div className="border-b border-[var(--border)] px-8 py-3">
        <div className="relative max-w-xs">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--foreground-disabled)]" />
          <input
            type="text"
            value={serviceFilter}
            onChange={(e) => setServiceFilter(e.target.value)}
            placeholder="Filter by service type..."
            className="w-full rounded-[var(--radius-md)] border border-[var(--border)] bg-[var(--background)] py-1.5 pl-9 pr-3 text-[13px] text-[var(--foreground)] placeholder:text-[var(--foreground-disabled)] focus:border-[var(--color-accent-7)] focus:outline-none"
          />
        </div>
      </div>

      <div className="px-8 py-4">
        <DataTable
          columns={columns}
          data={allOffers}
          isLoading={offers.isLoading}
          keyExtractor={(row) => row.id}
          emptyTitle={serviceFilter ? `No offers for "${serviceFilter}"` : "No offers"}
          emptyDescription="No standing offers found in the marketplace."
          totalLabel={`${allOffers.length} offers`}
        />
      </div>
    </div>
  );
}
