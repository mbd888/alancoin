import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { EmptyState } from "./empty-state";
import { Database } from "lucide-react";

describe("EmptyState", () => {
  it("renders title and description", () => {
    render(
      <EmptyState
        icon={Database}
        title="No items"
        description="Nothing to show here."
      />
    );

    expect(screen.getByText("No items")).toBeInTheDocument();
    expect(screen.getByText("Nothing to show here.")).toBeInTheDocument();
  });

  it("renders action when provided", () => {
    render(
      <EmptyState
        icon={Database}
        title="Empty"
        description="Nothing."
        action={<button>Create</button>}
      />
    );

    expect(screen.getByRole("button", { name: "Create" })).toBeInTheDocument();
  });
});
