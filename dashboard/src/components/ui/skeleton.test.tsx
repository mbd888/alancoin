import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { Skeleton, SkeletonRow, SkeletonCard } from "./skeleton";

describe("Skeleton", () => {
  it("renders with base classes", () => {
    const { container } = render(<Skeleton />);
    const el = container.firstChild as HTMLElement;
    expect(el.className).toContain("animate-pulse");
    expect(el.className).toContain("rounded-md");
    expect(el.className).toContain("bg-muted");
  });

  it("merges custom className", () => {
    const { container } = render(<Skeleton className="h-4 w-32" />);
    const el = container.firstChild as HTMLElement;
    expect(el.className).toContain("h-4");
    expect(el.className).toContain("w-32");
    expect(el.className).toContain("animate-pulse");
  });
});

describe("SkeletonRow", () => {
  it("renders 5 cells by default", () => {
    const { container } = render(
      <table>
        <tbody>
          <SkeletonRow />
        </tbody>
      </table>
    );
    const cells = container.querySelectorAll("td");
    expect(cells).toHaveLength(5);
  });

  it("renders custom number of cells", () => {
    const { container } = render(
      <table>
        <tbody>
          <SkeletonRow cols={3} />
        </tbody>
      </table>
    );
    const cells = container.querySelectorAll("td");
    expect(cells).toHaveLength(3);
  });

  it("each cell contains a skeleton div", () => {
    const { container } = render(
      <table>
        <tbody>
          <SkeletonRow cols={2} />
        </tbody>
      </table>
    );
    const skeletons = container.querySelectorAll(".animate-pulse");
    expect(skeletons).toHaveLength(2);
  });
});

describe("SkeletonCard", () => {
  it("renders three skeleton bars", () => {
    const { container } = render(<SkeletonCard />);
    const skeletons = container.querySelectorAll(".animate-pulse");
    expect(skeletons).toHaveLength(3);
  });

  it("has card-like styling", () => {
    const { container } = render(<SkeletonCard />);
    const el = container.firstChild as HTMLElement;
    expect(el.className).toContain("rounded-lg");
    expect(el.className).toContain("border");
    expect(el.className).toContain("bg-card");
  });
});
