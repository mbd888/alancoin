import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { copyToClipboard } from "@/lib/utils";
import { cn } from "@/lib/utils";

interface AddressProps {
  value: string;
  truncate?: boolean;
  className?: string;
}

export function Address({ value, truncate = true, className }: AddressProps) {
  const [copied, setCopied] = useState(false);

  const display = truncate && value.length > 16
    ? `${value.slice(0, 8)}...${value.slice(-4)}`
    : value;

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    await copyToClipboard(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <button
      type="button"
      onClick={handleCopy}
      title={value}
      className={cn(
        "group inline-flex items-center gap-1 rounded-sm font-mono text-xs transition-colors",
        "hover:text-foreground",
        className
      )}
    >
      {display}
      {copied ? (
        <Check size={10} className="text-success" />
      ) : (
        <Copy size={10} className="opacity-0 group-hover:opacity-50 transition-opacity" />
      )}
    </button>
  );
}
