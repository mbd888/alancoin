import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { useOffers } from "@/hooks/api/use-offers";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { OfferItem } from "@/lib/types";
import { Search, Store, AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/layouts/page-header";

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
        <span className="font-mono text-xs">{row.id.slice(0, 12)}...</span>
      ),
    },
    {
      id: "seller",
      header: "Seller",
      cell: (row) => (
        <span className="font-mono text-xs">
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
        <span className="text-xs">
          {row.remainingCap}
          <span className="text-muted-foreground/50"> / </span>
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
        <span className="text-xs text-muted-foreground">
          {relativeTime(row.expiresAt)}
        </span>
      ),
    },
    {
      id: "created",
      header: "Created",
      cell: (row) => (
        <span className="text-xs text-muted-foreground">
          {relativeTime(row.createdAt)}
        </span>
      ),
    },
  ];

  return (
    <div className="min-h-screen">
      <PageHeader icon={Store} title="Marketplace" description="Standing offers from service providers" />

      {/* Service type filter */}
      <div className="border-b px-4 md:px-8 py-3">
        <div className="relative max-w-xs">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground/50" />
          <Input
            id="service-filter"
            value={serviceFilter}
            onChange={(e) => setServiceFilter(e.target.value)}
            placeholder="Filter by service type..."
            className="pl-9"
          />
        </div>
      </div>

      <div className="px-4 md:px-8 py-4">
        {offers.isError ? (
          <div className="flex items-center justify-center gap-2 rounded-lg border bg-card py-8 text-sm text-destructive">
            <AlertTriangle size={14} />
            Failed to load offers
            <Button variant="ghost" size="sm" onClick={() => offers.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <DataTable
            columns={columns}
            data={allOffers}
            isLoading={offers.isLoading}
            keyExtractor={(row) => row.id}
            emptyTitle={serviceFilter ? `No offers for "${serviceFilter}"` : "No offers"}
            emptyDescription="No standing offers found in the marketplace."
            totalLabel={`${allOffers.length} offers`}
          />
        )}
      </div>
    </div>
  );
}
