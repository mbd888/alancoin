import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Dialog, DialogHeader, DialogBody, DialogFooter } from "./dialog";

describe("Dialog", () => {
  it("renders nothing when closed", () => {
    const { container } = render(
      <Dialog open={false} onClose={() => {}}>
        <DialogBody>Content</DialogBody>
      </Dialog>
    );

    expect(container.innerHTML).toBe("");
  });

  it("renders content when open", () => {
    render(
      <Dialog open onClose={() => {}}>
        <DialogBody>Hello dialog</DialogBody>
      </Dialog>
    );

    expect(screen.getByText("Hello dialog")).toBeInTheDocument();
  });

  it("renders header with close button", async () => {
    const onClose = vi.fn();
    render(
      <Dialog open onClose={onClose}>
        <DialogHeader onClose={onClose}>
          <h2>Title</h2>
        </DialogHeader>
        <DialogBody>Body</DialogBody>
      </Dialog>
    );

    expect(screen.getByText("Title")).toBeInTheDocument();

    // Click the X button
    const closeButtons = screen.getAllByRole("button");
    await userEvent.click(closeButtons[0]);
    expect(onClose).toHaveBeenCalled();
  });

  it("renders footer", () => {
    render(
      <Dialog open onClose={() => {}}>
        <DialogFooter>
          <button>Save</button>
        </DialogFooter>
      </Dialog>
    );

    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  });

  it("calls onClose on Escape", async () => {
    const onClose = vi.fn();
    render(
      <Dialog open onClose={onClose}>
        <DialogBody>Esc test</DialogBody>
      </Dialog>
    );

    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });
});
