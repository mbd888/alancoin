import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Input } from "./input";

describe("Input", () => {
  it("renders with label", () => {
    render(<Input id="test" label="Name" />);
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
  });

  it("shows error message", () => {
    render(<Input id="test" label="Email" error="Required" />);
    expect(screen.getByText("Required")).toBeInTheDocument();
  });

  it("accepts user input", async () => {
    render(<Input id="test" label="Name" />);
    const input = screen.getByLabelText("Name");

    await userEvent.type(input, "hello");
    expect(input).toHaveValue("hello");
  });

  it("passes placeholder", () => {
    render(<Input id="test" placeholder="Enter value" />);
    expect(screen.getByPlaceholderText("Enter value")).toBeInTheDocument();
  });
});
