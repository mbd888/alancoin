import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from "./card";

describe("Card", () => {
  it("renders children", () => {
    render(<Card>content</Card>);
    expect(screen.getByText("content")).toBeInTheDocument();
  });

  it("applies custom className", () => {
    const { container } = render(<Card className="mt-4">test</Card>);
    expect((container.firstChild as HTMLElement).className).toContain("mt-4");
  });

  it("includes default border and bg classes", () => {
    const { container } = render(<Card>test</Card>);
    const el = container.firstChild as HTMLElement;
    expect(el.className).toContain("rounded-lg");
    expect(el.className).toContain("border");
    expect(el.className).toContain("bg-card");
  });
});

describe("CardHeader", () => {
  it("renders children", () => {
    render(<CardHeader>header</CardHeader>);
    expect(screen.getByText("header")).toBeInTheDocument();
  });
});

describe("CardTitle", () => {
  it("renders children", () => {
    render(<CardTitle>title</CardTitle>);
    expect(screen.getByText("title")).toBeInTheDocument();
  });
});

describe("CardDescription", () => {
  it("renders children", () => {
    render(<CardDescription>desc</CardDescription>);
    expect(screen.getByText("desc")).toBeInTheDocument();
  });
});

describe("CardContent", () => {
  it("renders children", () => {
    render(<CardContent>body</CardContent>);
    expect(screen.getByText("body")).toBeInTheDocument();
  });
});

describe("CardFooter", () => {
  it("renders children", () => {
    render(<CardFooter>footer</CardFooter>);
    expect(screen.getByText("footer")).toBeInTheDocument();
  });

  it("applies custom className", () => {
    const { container } = render(<CardFooter className="gap-4">footer</CardFooter>);
    expect((container.firstChild as HTMLElement).className).toContain("gap-4");
  });
});
