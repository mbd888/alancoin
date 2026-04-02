import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Button } from "./button";

describe("Button", () => {
  it("renders children", () => {
    render(<Button>Click me</Button>);
    expect(screen.getByRole("button", { name: "Click me" })).toBeInTheDocument();
  });

  it("handles click", async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>Click</Button>);

    await userEvent.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("can be disabled", async () => {
    const onClick = vi.fn();
    render(<Button disabled onClick={onClick}>Disabled</Button>);

    const btn = screen.getByRole("button");
    expect(btn).toBeDisabled();
    await userEvent.click(btn);
    expect(onClick).not.toHaveBeenCalled();
  });

  it("applies variant styles", () => {
    const { container } = render(<Button variant="primary">Primary</Button>);
    const btn = container.firstChild as HTMLElement;
    expect(btn.className).toContain("bg-primary");
  });

  it("applies size styles", () => {
    const { container } = render(<Button size="lg">Large</Button>);
    const btn = container.firstChild as HTMLElement;
    expect(btn.className).toContain("h-9");
  });
});
