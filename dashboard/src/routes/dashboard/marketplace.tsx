import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogBody, DialogFooter } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { useOffers } from "@/hooks/api/use-offers";
import { formatCurrency, relativeTime } from "@/lib/utils";
import type { OfferItem } from "@/lib/types";
import { Search, Store, AlertTriangle, MoreHorizontal, Eye } from "lucide-react";
import { PageHeader } from "@/components/layouts/page-header";
import { Address } from "@/components/ui/address";

const STATUS_VARIANT: Record<string, string> = {
  active: "success",
  exhausted: "warning",
  cancelled: "default",
  expired: "default",
};

export function MarketplacePage() {
  const [serviceFilter, setServiceFilter] = useState("");
  const [viewOffer, setViewOffer] = useState<OfferItem | null>(null);
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
      cell: (row) => <Address value={row.sellerAddr} />,
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
      sortable: true,
      sortValue: (row) => parseFloat(row.price) || 0,
      cell: (row) => formatCurrency(row.price),
    },
    {
      id: "capacity",
      header: "Capacity",
      sortable: true,
      sortValue: (row) => row.remainingCap,
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
      sortable: true,
      sortValue: (row) => new Date(row.createdAt).getTime(),
      cell: (row) => (
        <span className="text-xs text-muted-foreground">
          {relativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      id: "actions",
      header: "",
      className: "w-10",
      cell: (row) => (
        <div onClick={(e) => e.stopPropagation()}>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button aria-label="Offer actions" className="rounded-sm p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setViewOffer(row)}>
                <Eye size={13} />
                View details
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      ),
    },
  ];

  return (
    <div className="min-h-screen">
      <PageHeader icon={Store} title="Marketplace" description="Standing offers from service providers" />

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
            onRowClick={(row) => setViewOffer(row)}
            dataUpdatedAt={offers.dataUpdatedAt}
            emptyTitle={serviceFilter ? `No offers for "${serviceFilter}"` : "No offers"}
            emptyDescription="No standing offers found in the marketplace."
            totalLabel={`${allOffers.length} offers`}
          />
        )}
      </div>

      <Dialog open={!!viewOffer} onOpenChange={() => setViewOffer(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Offer Details</DialogTitle>
            <DialogDescription>Marketplace standing offer information.</DialogDescription>
          </DialogHeader>
          {viewOffer && (
            <DialogBody>
              <div className="flex flex-col gap-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Offer ID</span>
                  <code className="text-right font-mono text-xs">{viewOffer.id}</code>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Seller</span>
                  <Address value={viewOffer.sellerAddr} truncate={false} />
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Status</span>
                  <Badge variant={(STATUS_VARIANT[viewOffer.status] ?? "default") as "success" | "warning" | "default"}>
                    {viewOffer.status}
                  </Badge>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Service Type</span>
                  <Badge variant="accent">{viewOffer.serviceType}</Badge>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Description</span>
                  <span className="text-right text-xs">{viewOffer.description}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Price</span>
                  <span className="tabular-nums">{formatCurrency(viewOffer.price)}</span>
                </div>
                <hr className="border-border" />
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Capacity</span>
                  <span className="tabular-nums">{viewOffer.remainingCap} / {viewOffer.capacity}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Expires</span>
                  <span>{relativeTime(viewOffer.expiresAt)}</span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-xs text-muted-foreground">Created</span>
                  <span>{relativeTime(viewOffer.createdAt)}</span>
                </div>
              </div>
            </DialogBody>
          )}
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setViewOffer(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
