import { useState, useCallback, useRef, useEffect } from "react";
import { Eye, EyeOff, Copy, Check } from "lucide-react";
import { cn, maskSecret, copyToClipboard } from "@/lib/utils";
import { toast } from "sonner";

interface SecretDisplayProps {
  value: string;
  label?: string;
  className?: string;
}

export function SecretDisplay({ value, label, className }: SecretDisplayProps) {
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  // Auto-hide after 30 seconds
  useEffect(() => {
    if (!revealed) return;
    timerRef.current = setTimeout(() => setRevealed(false), 30_000);
    return () => clearTimeout(timerRef.current);
  }, [revealed]);

  const handleCopy = useCallback(async () => {
    await copyToClipboard(value);
    setCopied(true);
    toast.success("Copied to clipboard");
    setTimeout(() => setCopied(false), 2000);
  }, [value]);

  return (
    <div className={cn("group flex items-center gap-2", className)}>
      {label && (
        <span className="shrink-0 text-xs text-[var(--foreground-muted)]">
          {label}
        </span>
      )}
      <button
        onClick={handleCopy}
        className={cn(
          "flex items-center gap-2 rounded-[var(--radius-md)] border border-[var(--border-subtle)]",
          "bg-[var(--background)] px-3 py-1.5",
          "transition-[border-color] duration-150",
          "hover:border-[var(--border)]"
        )}
      >
        <code className="select-all font-mono text-xs tabular-nums text-[var(--foreground-secondary)]">
          {revealed ? value : maskSecret(value)}
        </code>
        {copied ? (
          <Check size={13} className="shrink-0 text-[var(--color-success)]" />
        ) : (
          <Copy
            size={13}
            className="shrink-0 text-[var(--foreground-disabled)] opacity-0 transition-opacity duration-150 group-hover:opacity-100"
          />
        )}
      </button>
      <button
        onClick={() => setRevealed(!revealed)}
        className={cn(
          "rounded-[var(--radius-sm)] p-1",
          "text-[var(--foreground-disabled)]",
          "transition-[color] duration-150 hover:text-[var(--foreground-secondary)]"
        )}
        title={revealed ? "Hide" : "Reveal (hides after 30s)"}
      >
        {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
      </button>
    </div>
  );
}
