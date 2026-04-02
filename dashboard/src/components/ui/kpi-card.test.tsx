import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { KpiCard } from "./kpi-card";
import { Activity } from "lucide-react";

describe("KpiCard", () => {
  it("renders label and value", () => {
    render(<KpiCard label="Total" value={42} />);
    expect(screen.getByText("Total")).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
  });

  it("renders string value", () => {
    render(<KpiCard label="Revenue" value="$1,234" />);
    expect(screen.getByText("$1,234")).toBeInTheDocument();
  });

  it("renders icon when provided", () => {
    const { container } = render(<KpiCard label="Active" value={5} icon={Activity} />);
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("hides icon when not provided", () => {
    const { container } = render(<KpiCard label="Active" value={5} />);
    expect(container.querySelector("svg")).toBeNull();
  });

  it("renders change text when provided", () => {
    render(<KpiCard label="Score" value={85} change="+5.2%" changeType="positive" />);
    expect(screen.getByText("+5.2%")).toBeInTheDocument();
  });

  it("hides change when not provided", () => {
    const { container } = render(<KpiCard label="Score" value={85} />);
    // Only label and value should be text nodes
    expect(container.textContent).toBe("Score85");
  });

  it("applies positive color for positive changeType", () => {
    render(<KpiCard label="Score" value={85} change="+5%" changeType="positive" />);
    const change = screen.getByText("+5%");
    expect(change.className).toContain("text-success");
  });

  it("applies negative color for negative changeType", () => {
    render(<KpiCard label="Score" value={85} change="-3%" changeType="negative" />);
    const change = screen.getByText("-3%");
    expect(change.className).toContain("text-destructive");
  });

  it("applies neutral color by default", () => {
    render(<KpiCard label="Score" value={85} change="stable" />);
    const change = screen.getByText("stable");
    expect(change.className).toContain("text-muted-foreground");
  });
});
