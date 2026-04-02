import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogBody, DialogFooter } from "./dialog";

describe("Dialog", () => {
  it("renders nothing when closed", () => {
    render(
      <Dialog open={false} onOpenChange={() => {}}>
        <DialogContent>
          <DialogBody>Content</DialogBody>
        </DialogContent>
      </Dialog>
    );

    expect(screen.queryByText("Content")).not.toBeInTheDocument();
  });

  it("renders content when open", () => {
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogBody>Hello dialog</DialogBody>
        </DialogContent>
      </Dialog>
    );

    expect(screen.getByText("Hello dialog")).toBeInTheDocument();
  });

  it("renders header with close button", async () => {
    const onOpenChange = vi.fn();
    render(
      <Dialog open onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Title</DialogTitle>
          </DialogHeader>
          <DialogBody>Body</DialogBody>
        </DialogContent>
      </Dialog>
    );

    expect(screen.getByText("Title")).toBeInTheDocument();

    // Click the X close button (has sr-only "Close" text)
    const closeButton = screen.getByRole("button", { name: "Close" });
    await userEvent.click(closeButton);
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("renders footer", () => {
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogFooter>
            <button>Save</button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );

    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  });

  it("calls onOpenChange on Escape", async () => {
    const onOpenChange = vi.fn();
    render(
      <Dialog open onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogBody>Esc test</DialogBody>
        </DialogContent>
      </Dialog>
    );

    await userEvent.keyboard("{Escape}");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
