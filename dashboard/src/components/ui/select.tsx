import { cn } from "@/lib/utils";
import type { SelectHTMLAttributes } from "react";

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label?: string;
  options: { value: string; label: string }[];
}

export function Select({ label, options, className, id, ...props }: SelectProps) {
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
      <select
        id={id}
        className={cn(
          "h-9 rounded-[var(--radius-md)] border border-[var(--border)] bg-[var(--background)] px-3 text-[13px] text-[var(--foreground)]",
          "outline-none transition-[border-color] duration-150",
          "focus:border-[var(--ring)]",
          className
        )}
        {...props}
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  );
}
