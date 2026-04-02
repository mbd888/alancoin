/** Shared chart color palette for Recharts bar/area charts */
export const CHART_COLORS = [
  "oklch(0.55 0.16 250)",
  "oklch(0.60 0.14 250)",
  "oklch(0.65 0.12 250)",
  "oklch(0.50 0.10 250)",
  "oklch(0.45 0.08 250)",
];

/** Shared tooltip styling for all Recharts tooltips */
export const CHART_TOOLTIP_STYLE: React.CSSProperties = {
  background: "var(--card)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-md)",
  fontSize: 12,
  color: "var(--foreground)",
};

/** Shared gradient fill for area charts */
export const AREA_GRADIENT_ID = "fillArea";
export const AREA_STROKE_COLOR = "oklch(0.55 0.16 250)";
