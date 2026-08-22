import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OpenAPIOperationNavigator } from "./_components/openapi-operation-navigator";
import type { OpenAPIOperation } from "./_components/openapi-operation-selection";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

const operations: OpenAPIOperation[] = [
  {
    routeID: 9, routeSlug: "users", path: "/users/{id}", method: "GET",
    pathItem: {}, operation: { summary: "Read user" },
  },
  {
    routeID: 9, routeSlug: "users", path: "/users/{id}", method: "DELETE",
    pathItem: {}, operation: { summary: "Delete user" },
  },
  {
    routeID: 9, routeSlug: "users", path: "/users", method: "POST",
    pathItem: {}, operation: { summary: "Create user" },
  },
];

describe("OpenAPIOperationNavigator", () => {
  it("groups multiple methods under their original documented path on desktop", () => {
    render(<OpenAPIOperationNavigator operations={operations} selected={operations[0]} onSelect={() => {}} />);

    expect(screen.getByTestId("openapi-operation-navigator")).toBeInTheDocument();
    expect(screen.getAllByText("/users/{id}")).not.toHaveLength(0);
    expect(screen.getByRole("button", { name: /GET \/users\/\{id\}/ })).toHaveAttribute("aria-current", "true");
    expect(screen.getByRole("button", { name: /DELETE \/users\/\{id\}/ })).toBeInTheDocument();
  });

  it("uses normal in-flow buttons instead of a hover card or sheet on mobile", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<OpenAPIOperationNavigator operations={operations} selected={operations[0]} onSelect={onSelect} />);

    await user.click(screen.getByRole("button", { name: /POST \/users$/ }));
    expect(onSelect).toHaveBeenCalledWith(operations[2]);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("renders an in-flow empty state when the scoped document has no visible operations", () => {
    render(<OpenAPIOperationNavigator operations={[]} onSelect={() => {}} />);

    expect(screen.getByText("emptyOpenAPIOperations")).toBeInTheDocument();
  });
});
