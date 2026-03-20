import { cn } from "@/lib/utils";
import type { InputHTMLAttributes } from "react";

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
}

export function Input({ label, error, className, id, ...props }: InputProps) {
  return (
    <div className="flex flex-col gap-1.5">
      {label && (
        <label
          htmlFor={id}
          className="text-[13px] font-medium text-[var(--foreground)]"
        >
          {label}
        </label>
      )}
      <input
        id={id}
        className={cn(
          "h-9 rounded-[var(--radius-md)] border border-[var(--border)] bg-[var(--background)] px-3 text-[13px] text-[var(--foreground)]",
          "placeholder:text-[var(--foreground-disabled)]",
          "outline-none transition-[border-color,box-shadow] duration-150",
          "focus:border-[var(--ring)] focus:ring-1 focus:ring-[var(--ring)]",
          error && "border-[var(--color-danger)] focus:border-[var(--color-danger)] focus:ring-[var(--color-danger)]",
          className
        )}
        {...props}
      />
      {error && (
        <span className="text-[12px] text-[var(--color-danger)]">{error}</span>
      )}
    </div>
  );
}
