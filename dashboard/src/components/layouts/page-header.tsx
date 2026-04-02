import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";

interface PageHeaderProps {
  title: ReactNode;
  description?: string;
  icon?: LucideIcon;
  badge?: ReactNode;
  actions?: ReactNode;
}

export function PageHeader({
  title,
  description,
  icon: Icon,
  badge,
  actions,
}: PageHeaderProps) {
  return (
    <header className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-4 md:px-8 md:py-5">
      <div>
        <div className="flex items-center gap-2">
          {Icon && (
            <Icon size={18} strokeWidth={1.8} className="text-accent-foreground" />
          )}
          <h1 className="text-base font-semibold text-foreground">{title}</h1>
        </div>
        {(description || badge) && (
          <p className="mt-0.5 text-sm text-muted-foreground">
            {description}
            {description && badge && <> &middot; </>}
            {badge}
          </p>
        )}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </header>
  );
}
