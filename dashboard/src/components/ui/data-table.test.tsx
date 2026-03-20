import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { DataTable, type Column } from "./data-table";

interface TestRow {
  id: string;
  name: string;
  value: number;
}

const columns: Column<TestRow>[] = [
  { id: "name", header: "Name", cell: (row) => row.name },
  { id: "value", header: "Value", numeric: true, cell: (row) => row.value },
];

const testData: TestRow[] = [
  { id: "1", name: "Alice", value: 100 },
  { id: "2", name: "Bob", value: 200 },
  { id: "3", name: "Charlie", value: 300 },
];

describe("DataTable", () => {
  it("renders column headers", () => {
    render(
      <DataTable columns={columns} data={testData} keyExtractor={(r) => r.id} />
    );

    expect(screen.getByText("Name")).toBeInTheDocument();
    expect(screen.getByText("Value")).toBeInTheDocument();
  });

  it("renders data rows", () => {
    render(
      <DataTable columns={columns} data={testData} keyExtractor={(r) => r.id} />
    );

    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("Bob")).toBeInTheDocument();
    expect(screen.getByText("Charlie")).toBeInTheDocument();
    expect(screen.getByText("100")).toBeInTheDocument();
  });

  it("shows skeleton rows when loading", () => {
    const { container } = render(
      <DataTable columns={columns} data={[]} isLoading keyExtractor={(r) => r.id} />
    );

    // 8 skeleton rows are rendered by default
    const skeletonRows = container.querySelectorAll("tbody tr");
    expect(skeletonRows.length).toBe(8);
  });

  it("shows empty state when no data", () => {
    render(
      <DataTable
        columns={columns}
        data={[]}
        keyExtractor={(r) => r.id}
        emptyTitle="Nothing here"
        emptyDescription="No rows found."
      />
    );

    expect(screen.getByText("Nothing here")).toBeInTheDocument();
    expect(screen.getByText("No rows found.")).toBeInTheDocument();
  });

  it("shows pagination when hasNextPage", () => {
    render(
      <DataTable
        columns={columns}
        data={testData}
        keyExtractor={(r) => r.id}
        hasNextPage
        totalLabel="3 items"
      />
    );

    expect(screen.getByText("3 items")).toBeInTheDocument();
  });
});
