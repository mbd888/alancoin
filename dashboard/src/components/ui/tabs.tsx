import { cn } from "@/lib/utils";

interface Tab {
  id: string;
  label: string;
  count?: number;
}

interface TabsProps {
  tabs: Tab[];
  active: string;
  onChange: (id: string) => void;
}

export function Tabs({ tabs, active, onChange }: TabsProps) {
  return (
    <div className="flex gap-0.5 rounded-[var(--radius-md)] bg-[var(--background-elevated)] p-0.5">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          onClick={() => onChange(tab.id)}
          className={cn(
            "inline-flex items-center gap-1.5 rounded-[var(--radius-sm)] px-3 py-1.5 text-[12px] font-medium",
            "transition-[background-color,color] duration-150",
            active === tab.id
              ? "bg-[var(--background-interactive)] text-[var(--foreground)]"
              : "text-[var(--foreground-muted)] hover:text-[var(--foreground-secondary)]"
          )}
        >
          {tab.label}
          {tab.count !== undefined && (
            <span
              className={cn(
                "tabular-nums rounded-full px-1.5 py-0.5 text-[10px]",
                active === tab.id
                  ? "bg-[var(--color-gray-4)] text-[var(--foreground-secondary)]"
                  : "bg-[var(--color-gray-3)] text-[var(--foreground-muted)]"
              )}
            >
              {tab.count}
            </span>
          )}
        </button>
      ))}
    </div>
  );
}
