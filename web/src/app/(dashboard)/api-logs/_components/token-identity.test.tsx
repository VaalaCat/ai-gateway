import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { APILogTokenIdentity } from "./token-identity";

describe("APILogTokenIdentity", () => {
  it("shows the request-time token name snapshot", () => {
    render(<APILogTokenIdentity tokenID={7} tokenName="production" />);

    expect(screen.getByText("production")).toHaveAttribute("title", "#7");
    expect(screen.queryByText("#7")).not.toBeInTheDocument();
  });

  it("falls back to the stable token ID when the name snapshot is empty", () => {
    render(<APILogTokenIdentity tokenID={7} tokenName="" />);

    expect(screen.getByText("#7")).toBeInTheDocument();
  });

  it("keeps zero-valued legacy identity explicit", () => {
    render(<APILogTokenIdentity tokenID={0} tokenName="" />);

    expect(screen.getByText("#0")).toBeInTheDocument();
  });
});
