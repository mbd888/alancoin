import { useEffect, type ReactNode } from "react";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

interface DialogProps {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  className?: string;
}

export function Dialog({ open, onClose, children, className }: DialogProps) {
  useEffect(() => {
    if (!open) return;
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKey);
    return () => document.removeEventListener("keydown", handleKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/60"
        onClick={onClose}
      />
      {/* Panel */}
      <div
        className={cn(
          "relative z-10 w-full max-w-md rounded-[var(--radius-xl)] border border-[var(--border)] bg-[var(--background-elevated)] shadow-2xl",
          className
        )}
      >
        {children}
      </div>
    </div>
  );
}

export function DialogHeader({
  children,
  onClose,
}: {
  children: ReactNode;
  onClose?: () => void;
}) {
  return (
    <div className="flex items-center justify-between border-b border-[var(--border)] px-5 py-4">
      <div>{children}</div>
      {onClose && (
        <button
          onClick={onClose}
          className="rounded-[var(--radius-sm)] p-1 text-[var(--foreground-muted)] transition-[color] duration-150 hover:text-[var(--foreground)]"
        >
          <X size={16} />
        </button>
      )}
    </div>
  );
}

export function DialogBody({ children }: { children: ReactNode }) {
  return <div className="px-5 py-4">{children}</div>;
}

export function DialogFooter({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center justify-end gap-2 border-t border-[var(--border)] px-5 py-3">
      {children}
    </div>
  );
}
