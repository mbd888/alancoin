import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Tabs } from "./tabs";

const tabs = [
  { id: "all", label: "All", count: 10 },
  { id: "active", label: "Active", count: 5 },
  { id: "expired", label: "Expired", count: 3 },
];

describe("Tabs", () => {
  it("renders all tabs", () => {
    render(<Tabs tabs={tabs} active="all" onChange={() => {}} />);

    expect(screen.getByText("All")).toBeInTheDocument();
    expect(screen.getByText("Active")).toBeInTheDocument();
    expect(screen.getByText("Expired")).toBeInTheDocument();
  });

  it("shows count badges", () => {
    render(<Tabs tabs={tabs} active="all" onChange={() => {}} />);

    expect(screen.getByText("10")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("calls onChange on tab click", async () => {
    const onChange = vi.fn();
    render(<Tabs tabs={tabs} active="all" onChange={onChange} />);

    await userEvent.click(screen.getByText("Active"));
    expect(onChange).toHaveBeenCalledWith("active");
  });
});
