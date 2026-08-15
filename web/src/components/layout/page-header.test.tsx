import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

import { PageHeader } from "./page-header";

describe("PageHeader", () => {
  it("renders the only page heading with description and actions", () => {
    render(
      <PageHeader
        title="API Catalog"
        description="Find an API"
        actions={<Button>Refresh</Button>}
      />,
    );

    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(screen.getByRole("heading", { level: 1 })).toHaveClass(
      "text-2xl",
      "font-bold",
      "tracking-tight",
    );
    expect(screen.getByText("Find an API")).toHaveClass(
      "text-sm",
      "text-muted-foreground",
    );
    expect(
      screen.getByRole("button", { name: "Refresh" }),
    ).toBeInTheDocument();
  });

  it("keeps back action and metadata outside the heading text", () => {
    render(
      <PageHeader
        backAction={<Button>Back</Button>}
        title="Weather"
        metadata={<Badge>enabled</Badge>}
      />,
    );

    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
      "Weather",
    );
    expect(screen.getByRole("heading", { level: 1 })).not.toHaveTextContent(
      "enabled",
    );
  });

  it("wraps long mobile actions without overriding semantic colors", () => {
    render(
      <PageHeader
        title="Long title"
        actions={
          <>
            <Button>A</Button>
            <Button>B</Button>
          </>
        }
      />,
    );

    expect(screen.getByTestId("page-header-actions")).toHaveClass("flex-wrap");
    expect(screen.getByTestId("page-header").className).not.toMatch(
      /bg-(white|black)|text-(gray|slate)-/,
    );
  });
});
