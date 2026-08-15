import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { FormPageSkeleton } from "./form-entry";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

it("renders the requested form title while the shared form entry is loading", () => {
  render(<FormPageSkeleton titleKey="createService" />);

  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("createService");
});
