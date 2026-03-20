import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Badge } from "./badge";

describe("Badge", () => {
  it("renders children", () => {
    render(<Badge>active</Badge>);
    expect(screen.getByText("active")).toBeInTheDocument();
  });

  it("applies variant classes", () => {
    const { container } = render(<Badge variant="success">ok</Badge>);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain("text-");
  });

  it("applies custom className", () => {
    const { container } = render(<Badge className="ml-2">test</Badge>);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain("ml-2");
  });

  it("defaults to 'default' variant", () => {
    render(<Badge>default</Badge>);
    expect(screen.getByText("default")).toBeInTheDocument();
  });
});
